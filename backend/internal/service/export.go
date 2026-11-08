package service

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ce/report-system/backend/internal/config"
	"github.com/ce/report-system/backend/internal/model"
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
)

// ExportService 导出子系统（设计文档 §6）：
//   - 同步流式导出：行数 ≤ SyncRowLimit，直接向 HTTP 响应流式写出，内存占用 O(批量)
//   - 异步任务导出：大结果集走任务队列（Worker 数可配），进度落库，导出中心下载，按保留期清理
type ExportService struct {
	db     *gorm.DB
	engine *QueryEngine
	cfg    config.Export

	queue   chan int64
	startMu sync.Once
}

// NewExportService 创建导出服务（不启动 Worker，需显式 StartWorkers）
func NewExportService(db *gorm.DB, engine *QueryEngine, cfg config.Export) *ExportService {
	if cfg.Dir == "" {
		cfg.Dir = "./data/exports"
	}
	if cfg.SyncRowLimit <= 0 {
		cfg.SyncRowLimit = 10000
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}
	if cfg.RetentionDays <= 0 {
		cfg.RetentionDays = 7
	}
	if cfg.XlsxSheetMaxRow <= 0 {
		cfg.XlsxSheetMaxRow = 1000000
	}
	return &ExportService{
		db:     db,
		engine: engine,
		cfg:    cfg,
		queue:  make(chan int64, 100),
	}
}

// ExportOutcome 导出请求结果：同步 or 异步任务
type ExportOutcome struct {
	Sync    bool              `json:"sync"`    // true=直接流式返回；false=异步任务
	Total   int64             `json:"total"`   // 预估总行数
	Task    *model.ExportTask `json:"task"`    // 异步任务详情
}

// Export 导出入口：预估行数决定同步/异步。异步入库任务并入队，返回任务详情。
func (s *ExportService) Export(ctx context.Context, reportID int64, format string, input map[string]any, sortCol, sortDir string, userID int64, username string) (*ExportOutcome, error) {
	format = strings.ToLower(format)
	if format != "xlsx" && format != "csv" {
		return nil, fmt.Errorf("导出格式仅支持 xlsx / csv")
	}
	report, _, _, err := s.engine.rpt.GetFull(reportID)
	if err != nil {
		return nil, err
	}
	total, err := s.engine.EstimateRows(ctx, reportID, input)
	if err != nil {
		return nil, err
	}
	if total <= s.cfg.SyncRowLimit {
		return &ExportOutcome{Sync: true, Total: total}, nil
	}

	// 异步任务
	snapshot, _ := json.Marshal(map[string]any{
		"params": input, "sortCol": sortCol, "sortDir": sortDir,
	})
	task := &model.ExportTask{
		ReportID: reportID, ReportName: report.Name, Format: format,
		Status: "queued", TotalRows: total, ParamsSnapshot: string(snapshot),
		CreatedBy: userID,
	}
	if err := s.db.Create(task).Error; err != nil {
		return nil, err
	}
	s.Enqueue(task.ID)
	return &ExportOutcome{Sync: false, Total: total, Task: task}, nil
}

// SyncFileName 生成同步导出的文件名（供 handler 预设响应头）
func (s *ExportService) SyncFileName(reportID int64, format string) string {
	report, _, _, err := s.engine.rpt.GetFull(reportID)
	if err != nil {
		return "export." + format
	}
	return fmt.Sprintf("%s_%s.%s", sanitizeName(report.Name), time.Now().Format("20060102_150405"), format)
}

// StreamSync 同步流式导出，直接写入 w，返回文件名与实际行数
func (s *ExportService) StreamSync(ctx context.Context, w io.Writer, reportID int64, format string, input map[string]any, sortCol, sortDir string) (string, int64, error) {
	report, _, _, err := s.engine.rpt.GetFull(reportID)
	if err != nil {
		return "", 0, err
	}
	name := fmt.Sprintf("%s_%s.%s", sanitizeName(report.Name), time.Now().Format("20060102_150405"), format)
	rows, err := s.stream(ctx, w, reportID, format, input, sortCol, sortDir, 0, report.ExportLimit, nil)
	return name, rows, err
}

// StreamScoped 导出指定范围（当前页/指定页）：从 offset 行起取 limit 行，同步流式写出。
// limit 受报表导出行上限（ExportLimit）约束。
func (s *ExportService) StreamScoped(ctx context.Context, w io.Writer, reportID int64, format string, input map[string]any, sortCol, sortDir string, offset, limit int64) (string, int64, error) {
	report, _, _, err := s.engine.rpt.GetFull(reportID)
	if err != nil {
		return "", 0, err
	}
	if limit <= 0 {
		limit = 1000
	}
	if report.ExportLimit > 0 && limit > report.ExportLimit {
		limit = report.ExportLimit
	}
	name := fmt.Sprintf("%s_%s.%s", sanitizeName(report.Name), time.Now().Format("20060102_150405"), format)
	rows, err := s.stream(ctx, w, reportID, format, input, sortCol, sortDir, offset, limit, nil)
	return name, rows, err
}

// stream 导出核心：流式写出，内存占用恒定；从 offset 行起最多 maxRows 行；progress 非 nil 时每 1 万行回调一次
func (s *ExportService) stream(ctx context.Context, w io.Writer, reportID int64, format string, input map[string]any, sortCol, sortDir string, offset, maxRows int64, progress func(done int64)) (int64, error) {
	report, _, _, err := s.engine.rpt.GetFull(reportID)
	if err != nil {
		return 0, err
	}
	if maxRows <= 0 {
		maxRows = report.ExportLimit
	}
	if maxRows <= 0 {
		maxRows = 5000000
	}
	var outIdx []int
	rows, outCols, err := s.engine.PrepareExport(ctx, reportID, input, sortCol, sortDir, offset, maxRows, &outIdx)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	// Scan 目标必须覆盖结果集全部列（SQL 结果可能含未配置/隐藏列，
	// 列数与输出列数不等）——取值后再按 outIdx（输出列 → 结果列下标）映射
	resultCols, err := rows.Columns()
	if err != nil {
		return 0, err
	}
	raw := make([]any, len(resultCols))
	ptrs := make([]any, len(resultCols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	headers := make([]string, len(outCols))
	for i, c := range outCols {
		if c.Label != "" {
			headers[i] = c.Label
		} else {
			headers[i] = c.Name
		}
	}
	// 列翻译：绑定字典的列按「导出显示」格式转换
	translators := s.engine.DictTranslators(ctx, outCols)

	var count int64
	switch format {
	case "csv":
		count, err = s.writeCSV(w, rows, ptrs, raw, outIdx, headers, translators, progress)
	default:
		count, err = s.writeXLSX(w, rows, ptrs, raw, outIdx, headers, translators, progress)
	}
	return count, err
}

// writeCSV CSV 流式写出（UTF-8 BOM 保证 Excel 打开中文不乱码）。
// ptrs/raw 覆盖结果集全部列，outIdx 为输出列在结果集中的下标映射
func (s *ExportService) writeCSV(w io.Writer, rows *sql.Rows, ptrs, raw []any, outIdx []int, headers []string, translators []func(any) any, progress func(int64)) (int64, error) {
	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return 0, err
	}
	bw := bufio.NewWriterSize(w, 256*1024)
	defer bw.Flush()
	cw := csv.NewWriter(bw)
	cw.Comma = ','
	if err := cw.Write(headers); err != nil {
		return 0, err
	}
	var count int64
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return count, err
		}
		rec := make([]string, len(outIdx))
		for i, idx := range outIdx {
			nv := normalizeValue(raw[idx])
			if translators != nil && translators[i] != nil && nv != nil {
				nv = translators[i](nv)
			}
			if nv == nil {
				rec[i] = ""
			} else {
				rec[i] = fmt.Sprint(nv)
			}
		}
		if err := cw.Write(rec); err != nil {
			return count, err
		}
		count++
		if count%2000 == 0 {
			cw.Flush()
		}
		if progress != nil && count%10000 == 0 {
			progress(count)
		}
	}
	if err := rows.Err(); err != nil {
		return count, err
	}
	cw.Flush()
	return count, cw.Error()
}

// writeXLSX xlsx 流式写出（StreamWriter，超单 sheet 上限自动拆分 Sheet2/3...）。
// ptrs/raw 覆盖结果集全部列，outIdx 为输出列在结果集中的下标映射
func (s *ExportService) writeXLSX(w io.Writer, rows *sql.Rows, ptrs, raw []any, outIdx []int, headers []string, translators []func(any) any, progress func(int64)) (int64, error) {
	f := excelize.NewFile()
	defer f.Close()
	sheet := "Sheet1"
	sw, err := f.NewStreamWriter(sheet)
	if err != nil {
		return 0, err
	}
	// 表头
	if err := setStreamRow(sw, 1, toIface(headers)); err != nil {
		return 0, err
	}
	var count int64
	sheetRow := 1 // 当前 sheet 已写行数（含表头）
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return count, err
		}
		rec := make([]any, len(outIdx))
		for i, idx := range outIdx {
			nv := normalizeValue(raw[idx])
			if translators != nil && translators[i] != nil && nv != nil {
				nv = translators[i](nv)
			}
			rec[i] = nv
		}
		count++
		sheetRow++
		if err := setStreamRow(sw, sheetRow, rec); err != nil {
			return count, err
		}
		// 单 sheet 行数上限：冲刷后开新 sheet
		if sheetRow >= int(s.cfg.XlsxSheetMaxRow) {
			if err := sw.Flush(); err != nil {
				return count, err
			}
			sheet = fmt.Sprintf("Sheet%d", count/int64(s.cfg.XlsxSheetMaxRow)+2)
			if _, err := f.NewSheet(sheet); err != nil {
				return count, err
			}
			sw, err = f.NewStreamWriter(sheet)
			if err != nil {
				return count, err
			}
			if err := setStreamRow(sw, 1, toIface(headers)); err != nil {
				return count, err
			}
			sheetRow = 1
		}
		if progress != nil && count%10000 == 0 {
			progress(count)
		}
	}
	if err := rows.Err(); err != nil {
		return count, err
	}
	if err := sw.Flush(); err != nil {
		return count, err
	}
	if _, werr := f.WriteTo(w); werr != nil { // StreamWriter 已冲刷，WriteTo 不在内存攒数据
		return count, werr
	}
	return count, nil
}

func setStreamRow(sw *excelize.StreamWriter, row int, vals []any) error {
	cell, err := excelize.CoordinatesToCellName(1, row)
	if err != nil {
		return err
	}
	cells := make([]any, len(vals))
	for i, v := range vals {
		if v == nil {
			cells[i] = &excelize.Cell{Value: ""}
		} else {
			cells[i] = &excelize.Cell{Value: v}
		}
	}
	return sw.SetRow(cell, cells)
}

func toIface(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// ---------- 异步任务 ----------

// Enqueue 任务入队（队列满则后台阻塞投递，不阻塞请求）
func (s *ExportService) Enqueue(taskID int64) {
	go func() { s.queue <- taskID }()
}

// StartWorkers 启动异步导出 Worker
func (s *ExportService) StartWorkers() {
	s.startMu.Do(func() {
		for i := 0; i < s.cfg.Workers; i++ {
			go s.worker(i)
		}
	})
}

func (s *ExportService) worker(no int) {
	for taskID := range s.queue {
		s.runTask(taskID)
	}
	_ = no
}

// runTask 执行单个导出任务
func (s *ExportService) runTask(taskID int64) {
	var task model.ExportTask
	if err := s.db.First(&task, taskID).Error; err != nil {
		return
	}
	if task.Status != "queued" {
		return
	}
	s.db.Model(&task).Updates(map[string]any{"status": "running"})

	var snap struct {
		Params  map[string]any `json:"params"`
		SortCol string         `json:"sortCol"`
		SortDir string         `json:"sortDir"`
	}
	_ = json.Unmarshal([]byte(task.ParamsSnapshot), &snap)

	if err := os.MkdirAll(s.cfg.Dir, 0o755); err != nil {
		s.failTask(&task, "创建导出目录失败: "+err.Error())
		return
	}
	fileName := fmt.Sprintf("%s_task%d_%s.%s", sanitizeName(task.ReportName), task.ID, time.Now().Format("20060102_150405"), task.Format)
	path := filepath.Join(s.cfg.Dir, fileName)

	progress := func(done int64) {
		pct := 0
		if task.TotalRows > 0 {
			pct = int(done * 95 / task.TotalRows) // 预留 5% 给落盘收尾
			if pct > 95 {
				pct = 95
			}
		}
		s.db.Model(&model.ExportTask{}).Where("id = ?", task.ID).
			Updates(map[string]any{"progress": pct, "total_rows": done})
	}

	f, err := os.Create(path)
	if err != nil {
		s.failTask(&task, "创建文件失败: "+err.Error())
		return
	}
	rows, werr := s.stream(context.Background(), f, task.ReportID, task.Format, snap.Params, snap.SortCol, snap.SortDir, 0, 0, progress)
	f.Close()
	if werr != nil {
		_ = os.Remove(path)
		s.failTask(&task, werr.Error())
		return
	}
	info, _ := os.Stat(path)
	now := time.Now()
	s.db.Model(&task).Updates(map[string]any{
		"status": "done", "progress": 100, "total_rows": rows,
		"file_path": path, "file_name": fileName,
		"file_size": fileSizeOf(info), "finished_at": &now,
	})
}

func (s *ExportService) failTask(task *model.ExportTask, msg string) {
	now := time.Now()
	msg = truncate(msg, 480)
	s.db.Model(task).Updates(map[string]any{
		"status": "failed", "error_msg": msg, "finished_at": &now,
	})
}

// ListTasks 任务列表：管理员全量，普通用户仅本人
func (s *ExportService) ListTasks(isAdmin bool, userID int64) ([]model.ExportTask, error) {
	var list []model.ExportTask
	q := s.db.Model(&model.ExportTask{}).Order("id DESC").Limit(200)
	if !isAdmin {
		q = q.Where("created_by = ?", userID)
	}
	err := q.Find(&list).Error
	return list, err
}

// GetTask 任务详情
func (s *ExportService) GetTask(id int64) (*model.ExportTask, error) {
	var t model.ExportTask
	if err := s.db.First(&t, id).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// FilePathForDownload 校验归属后返回文件路径（管理员不限）
func (s *ExportService) FilePathForDownload(id int64, isAdmin bool, userID int64) (string, string, error) {
	t, err := s.GetTask(id)
	if err != nil {
		return "", "", err
	}
	if !isAdmin && t.CreatedBy != userID {
		return "", "", fmt.Errorf("无权下载该任务文件")
	}
	if t.Status != "done" || t.FilePath == "" {
		return "", "", fmt.Errorf("任务未完成或无文件")
	}
	if _, err := os.Stat(t.FilePath); err != nil {
		return "", "", fmt.Errorf("导出文件已过期或不存在")
	}
	return t.FilePath, t.FileName, nil
}

// StartCleanup 启动过期文件清理（每 6 小时执行一次，删除超过保留期的已完成任务）
func (s *ExportService) StartCleanup() {
	go func() {
		for {
			s.cleanupOnce()
			time.Sleep(6 * time.Hour)
		}
	}()
}

func (s *ExportService) cleanupOnce() {
	cutoff := time.Now().AddDate(0, 0, -s.cfg.RetentionDays)
	var tasks []model.ExportTask
	if err := s.db.Where("finished_at IS NOT NULL AND finished_at < ?", cutoff).Find(&tasks).Error; err != nil {
		return
	}
	for _, t := range tasks {
		if t.FilePath != "" {
			_ = os.Remove(t.FilePath)
		}
		s.db.Delete(&model.ExportTask{}, t.ID)
	}
}

// ---------- 工具 ----------

// WriteRows 将前端回传的勾选行数据写出为文件（导出当前勾选行）。
// columns 为展示列标题，rows 与列一一对应；数据量小（≤单页行数），直接内存写出。
func (s *ExportService) WriteRows(w io.Writer, format string, columns []string, rows [][]any) (int64, error) {
	switch format {
	case "csv":
		return s.writeRowsCSV(w, columns, rows)
	default:
		return s.writeRowsXLSX(w, columns, rows)
	}
}

// writeRowsCSV 勾选行 CSV 写出（UTF-8 BOM 保证 Excel 打开中文不乱码）
func (s *ExportService) writeRowsCSV(w io.Writer, columns []string, rows [][]any) (int64, error) {
	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return 0, err
	}
	bw := bufio.NewWriterSize(w, 64*1024)
	defer bw.Flush()
	cw := csv.NewWriter(bw)
	if err := cw.Write(columns); err != nil {
		return 0, err
	}
	for _, row := range rows {
		rec := make([]string, len(columns))
		for i := range columns {
			if i < len(row) && row[i] != nil {
				rec[i] = fmt.Sprint(row[i])
			}
		}
		if err := cw.Write(rec); err != nil {
			return 0, err
		}
	}
	cw.Flush()
	return int64(len(rows)), cw.Error()
}

// writeRowsXLSX 勾选行 xlsx 写出（数据量小，用普通模式写出）
func (s *ExportService) writeRowsXLSX(w io.Writer, columns []string, rows [][]any) (int64, error) {
	f := excelize.NewFile()
	defer f.Close()
	sheet := "Sheet1"
	if err := f.SetSheetRow(sheet, "A1", toIface(columns)); err != nil {
		return 0, err
	}
	for i, row := range rows {
		cells := make([]any, len(columns))
		for j := range columns {
			if j < len(row) && row[j] != nil {
				cells[j] = row[j]
			} else {
				cells[j] = ""
			}
		}
		cell, err := excelize.CoordinatesToCellName(1, i+2)
		if err != nil {
			return 0, err
		}
		if err := f.SetSheetRow(sheet, cell, cells); err != nil {
			return 0, err
		}
	}
	if _, werr := f.WriteTo(w); werr != nil {
		return 0, werr
	}
	return int64(len(rows)), nil
}


func sanitizeName(name string) string {
	repl := strings.NewReplacer("\\", "_", "/", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_", " ", "_")
	out := repl.Replace(name)
	if out == "" {
		out = "report"
	}
	return out
}

func fileSizeOf(info os.FileInfo) int64 {
	if info == nil {
		return 0
	}
	return info.Size()
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
