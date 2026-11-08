<!--
  Config.vue - 报表配置 - 在线报表系统

  布局：顶栏（返回 / 标题 / 状态 / 保存·预览·发布·公开）+ 左侧步骤导航 + 右侧滚动内容。
  入口：从「报表中心」页的按钮进入（左侧菜单不再显示），顶栏提供返回按钮。

  基础信息支持两种取数方式：
    1) 手写 SQL —— 内置 SqlEditor（高亮 / 补全 / 格式化，零外部依赖）
    2) 选表生成 —— 选数据源 → 搜表 → 勾字段 → 自动生成 SELECT 并填充展示列
-->
<template>
  <div class="cfg-page">
    <!-- 顶栏：返回 + 标题 + 操作（固定不滚动，长表单里也能随时保存） -->
    <div class="cfg-header">
      <el-button link type="primary" class="cfg-back" @click="back">
        <el-icon style="margin-right:3px"><ArrowLeft /></el-icon>返回报表中心
      </el-button>
      <span class="cfg-header-title">{{ form.name || '新建报表' }}</span>
      <el-tag v-if="id" size="small" :type="statusType">{{ statusText }}</el-tag>
      <span class="cfg-header-id muted" v-if="id">#{{ id }}</span>
      <div class="cfg-header-actions">
        <el-button v-if="id && can('report:public')" @click="publicVisible=true">公开访问</el-button>
        <el-button v-if="id && can('report:publish')"
                   :type="report.status==='published' ? 'warning' : 'success'" plain
                   @click="setStatus(report.status==='published' ? 'disabled' : 'published')">
          {{ report.status==='published' ? '停用' : '发布' }}
        </el-button>
        <el-button v-if="can('report:preview')" :disabled="!id" :loading="previewing" @click="preview">预览</el-button>
        <el-button v-if="id ? can('report:update') : can('report:create')"
                   type="primary" :loading="saving" @click="save">保存</el-button>
      </div>
    </div>

    <div class="cfg-body">
      <!-- 左：步骤导航（替代原来的横向页签，纵向排布更省横向空间） -->
      <nav class="cfg-nav">
        <div v-for="s in steps" :key="s.name" class="cfg-nav-item"
             :class="{ 'is-active': tab === s.name }" @click="tab = s.name">
          <span class="cfg-nav-idx">{{ s.idx }}</span>
          <span class="cfg-nav-label">{{ s.label }}</span>
          <span class="cfg-nav-count" v-if="s.count !== null">{{ s.count }}</span>
        </div>
        <div class="cfg-nav-foot muted">{{ id ? '修改后记得保存' : '保存后才可预览 / 发布' }}</div>
      </nav>

      <!-- 右：内容（整块滚动，顶部操作条不动） -->
      <section class="cfg-main" v-loading="loading">
        <!-- ============ 基础信息 ============ -->
        <div v-show="tab === 'base'" class="cfg-pane">
          <el-card shadow="never" class="cfg-card">
            <template #header><span class="cfg-card-title">基本信息</span></template>
            <el-form label-width="96px" class="cfg-form">
              <div class="cfg-grid">
                <el-form-item label="报表名称" required>
                  <el-input v-model="form.name" placeholder="给报表起个名字"></el-input>
                </el-form-item>
                <el-form-item label="所属分组" required>
                  <el-select v-model="form.groupId" placeholder="选择分组" style="flex:1">
                    <el-option v-for="g in groups" :key="g.id" :label="g.name" :value="g.id"></el-option>
                  </el-select>
                  <el-button size="small" style="margin-left:8px" @click="addGroup">新建</el-button>
                </el-form-item>
              </div>
              <el-form-item label="备注">
                <el-input v-model="form.remark" placeholder="用途说明，会显示在报表标题旁"></el-input>
              </el-form-item>
            </el-form>
          </el-card>

          <el-card shadow="never" class="cfg-card">
            <template #header>
              <div class="cfg-card-head">
                <span class="cfg-card-title">取数配置</span>
                <el-radio-group v-model="pick.mode" size="small" @change="onModeChange">
                  <el-radio-button value="sql">手写 SQL</el-radio-button>
                  <el-radio-button value="table">选表生成</el-radio-button>
                </el-radio-group>
              </div>
            </template>
            <el-form label-width="96px" class="cfg-form">
              <el-form-item label="数据源" required>
                <el-select v-model="form.datasourceId" placeholder="选择数据源" style="width:340px"
                           @change="onDatasourceChange">
                  <el-option v-for="d in datasources" :key="d.id"
                             :label="d.name + '（' + d.dbType + '）'" :value="d.id"></el-option>
                </el-select>
                <span class="muted" style="margin-left:10px" v-if="pick.mode === 'table'">
                  浏览表结构需要「数据源查看」权限，没有权限时请手写 SQL
                </span>
              </el-form-item>

              <!-- 选表：数据源 → 表 → 勾字段 → 生成 SQL -->
              <template v-if="pick.mode === 'table'">
                <el-form-item label="数据表">
                  <el-select v-model="pick.table" filterable remote clearable
                             :remote-method="searchTables" :loading="pick.loadingTables"
                             style="width:400px" placeholder="输入表名关键字搜索"
                             @change="onTableChange"
                             @visible-change="(v) => { if (v && !pick.tables.length) loadTables('') }">
                    <el-option v-for="t in pick.tables" :key="t.name" :label="t.name" :value="t.name">
                      <span>{{ t.name }}</span>
                      <span class="cfg-opt-note">{{ t.comment || t.tableType }}</span>
                    </el-option>
                  </el-select>
                  <el-button size="small" style="margin-left:8px" :loading="pick.loadingTables"
                             @click="loadTables('', true)">刷新表清单</el-button>
                  <span class="muted" style="margin-left:8px">
                    {{ pick.total }} 张{{ pick.truncated ? '（已截断，请用关键字精确搜索）' : '' }}
                    <template v-if="cacheAgoText">· {{ cacheAgoText }}</template>
                  </span>
                </el-form-item>

                <el-form-item label="输出字段" v-if="pick.columns.length">
                  <div class="cfg-picker">
                    <div class="cfg-picker-bar">
                      <el-checkbox :model-value="allPicked" :indeterminate="somePicked"
                                   @change="toggleAllCols">
                        全选（已选 {{ pick.cols.length }} / 共 {{ pick.columns.length }}）
                      </el-checkbox>
                      <div>
                        <el-button size="small" :loading="idx.loading" @click="openIndexes">
                          查看索引{{ idx.list.length ? '（' + idx.list.length + '）' : '' }}
                        </el-button>
                        <el-button size="small" type="primary" @click="applyPick">
                          生成 SQL 并填充展示列
                        </el-button>
                      </div>
                    </div>
                    <div class="cfg-picker-list">
                      <el-checkbox v-for="c in pick.columns" :key="c.name"
                                   :model-value="pick.cols.indexOf(c.name) >= 0"
                                   class="cfg-picker-item"
                                   @change="(v) => toggleCol(c.name, v)">
                        <span class="mono">{{ c.name }}</span>
                        <span class="muted" style="margin-left:6px">{{ (c.comment || '').trim() || c.dataType }}</span>
                      </el-checkbox>
                    </div>
                  </div>
                </el-form-item>
                <el-form-item label="生成结果">
                  <pre class="cfg-sql-preview mono">{{ form.sqlText || '（选表并点「生成 SQL」后在此预览）' }}</pre>
                </el-form-item>
              </template>

              <!-- 手写 SQL -->
              <template v-else>
                <el-form-item label="查询 SQL" required>
                  <div class="cfg-sql-wrap">
                    <SqlEditor ref="sqlEditor" v-model="form.sqlText" :height="330"
                               :tables="tableNames" :columns="tableColumnNames" :params="paramNames"
                               placeholder="SELECT id, name FROM orders WHERE status = :status"></SqlEditor>
                    <div class="cfg-sql-help muted">
                      占位符：<code>:paramName</code>；范围条件 <code>:name_1</code> / <code>:name_2</code>；
                      IN 条件 <code>:name</code> 自动展开。不写占位符的条件走「自动装配」，用展示列的「查询」开关即可。
                      <span v-if="paramNames.length">
                       　插入：<el-button v-for="pn in paramNames" :key="pn" size="small" link type="primary"
                                        @click="insertParam(pn)">{{ ':' + pn }}</el-button>
                      </span>
                    </div>
                  </div>
                </el-form-item>
              </template>
            </el-form>
          </el-card>

          <el-card shadow="never" class="cfg-card">
            <template #header><span class="cfg-card-title">运行参数</span></template>
            <el-form label-width="96px" class="cfg-form">
              <div class="cfg-grid cfg-grid-4">
                <el-form-item label="查询超时">
                  <el-input-number v-model="form.queryTimeout" :min="1" :max="600" :controls="false"
                                   style="width:90px"></el-input-number>
                  <span class="muted" style="margin-left:6px">秒</span>
                </el-form-item>
                <el-form-item label="行数上限">
                  <el-input-number v-model="form.rowLimit" :min="1" :controls="false" style="width:120px"></el-input-number>
                </el-form-item>
                <el-form-item label="导出行上限">
                  <el-input-number v-model="form.exportLimit" :min="1" :controls="false" style="width:130px"></el-input-number>
                </el-form-item>
                <el-form-item label="默认排序">
                  <el-input v-model="form.defaultOrder" placeholder="结果列名" style="width:120px"></el-input>
                </el-form-item>
              </div>
            </el-form>
          </el-card>
        </div>

        <!-- ============ 查询条件 ============ -->
        <div v-show="tab === 'params'" class="cfg-pane">
          <div class="toolbar">
            <el-button size="small" type="primary" @click="addParam">添加条件</el-button>
            <span class="muted">两种装配方式：① 在 SQL 中写 :name 占位符（like 自动加 %，in 自动展开，range 拆 :name_1/:name_2）② 不写占位符，系统自动包装外层 WHERE（展示列开启「查询」开关的条件走这种方式）</span>
          </div>
          <el-table :data="form.params" border size="small">
            <el-table-column label="序" width="64" align="center">
              <template #default="s">
                <!-- 拖动调整顺序：保存时按数组下标写 sortNo，报表页按 sort_no 排序渲染 -->
                <span class="drag-handle" draggable="true" title="拖动调整顺序"
                      @dragstart="dragStart(s.row, form.params)" @dragover.prevent
                      @drop.prevent="drop(s.row, form.params)" @dragend="dragEnd()">⠿</span>
                <span style="margin-left:2px">{{ s.$index + 1 }}</span>
              </template>
            </el-table-column>
            <el-table-column label="参数名(对应 :name)" width="170">
              <template #default="s"><el-input v-model="s.row.paramName" size="small" class="mono"></el-input></template>
            </el-table-column>
            <el-table-column label="列名(结果集)" width="140">
              <template #default="s"><el-input v-model="s.row.columnName" size="small" placeholder="默认=参数名"></el-input></template>
            </el-table-column>
            <el-table-column label="显示名" width="130">
              <template #default="s"><el-input v-model="s.row.label" size="small"></el-input></template>
            </el-table-column>
            <el-table-column label="控件" width="150">
              <template #default="s">
                <el-select v-model="s.row.widget" size="small">
                  <el-option v-for="w in widgets" :key="w.v" :label="w.t" :value="w.v"></el-option>
                </el-select>
              </template>
            </el-table-column>
            <el-table-column label="匹配方式" width="110">
              <template #default="s">
                <el-select v-model="s.row.matchType" size="small">
                  <el-option label="等于" value="eq"></el-option>
                  <el-option label="模糊" value="like"></el-option>
                  <el-option label="包含" value="in"></el-option>
                  <el-option label="范围" value="range"></el-option>
                </el-select>
              </template>
            </el-table-column>
            <el-table-column label="必填" width="60">
              <template #default="s"><el-switch v-model="s.row.required" :active-value="1" :inactive-value="0" size="small"></el-switch></template>
            </el-table-column>
            <el-table-column label="默认值" width="150">
              <template #default="s">
                <!-- 下拉控件 + 字典来源：默认值直接从字典选项里选；多选控件可选多个（存库逗号拼接） -->
                <el-select v-if="isDictDefault(s.row)"
                           :model-value="defaultValModel(s.row)"
                           :multiple="s.row.widget === 'multiSelect'"
                           collapse-tags collapse-tags-tooltip
                           clearable filterable allow-create default-first-option
                           placeholder="选择字典选项" style="width:100%"
                           @change="(v) => setDefaultVal(s.row, v)"
                           @visible-change="(v) => { if (v) loadDictOptions(s.row) }">
                  <el-option v-for="o in dictOptionsOf(s.row)" :key="String(o.value)"
                             :label="o.label" :value="String(o.value)"></el-option>
                </el-select>
                <el-input v-else v-model="s.row.defaultVal" size="small" :disabled="!!s.row.defaultMacro"></el-input>
              </template>
            </el-table-column>
            <el-table-column label="动态宏" width="130">
              <template #default="s">
                <el-select v-model="s.row.defaultMacro" size="small" clearable>
                  <el-option label="今天" value="today"></el-option>
                  <el-option label="昨天" value="yesterday"></el-option>
                  <el-option label="近7天" value="last7days"></el-option>
                  <el-option label="近30天" value="last30days"></el-option>
                  <el-option label="本月" value="thisMonth"></el-option>
                </el-select>
              </template>
            </el-table-column>
            <el-table-column label="选项来源" width="100">
              <template #default="s">
                <el-select v-model="s.row.optionType" size="small">
                  <el-option label="固定" value="fixed"></el-option>
                  <el-option label="SQL" value="sql"></el-option>
                  <el-option label="字典" value="dict"></el-option>
                </el-select>
              </template>
            </el-table-column>
            <el-table-column label="固定选项(JSON) / 选项SQL / 字典" min-width="220">
              <template #default="s">
                <el-input v-if="s.row.optionType==='fixed'" v-model="s.row.optionFixed" size="small" class="mono"
                          placeholder='[{"value":1,"label":"启用"}]'></el-input>
                <el-select v-else-if="s.row.optionType==='dict'" v-model="s.row.optionDict" size="small"
                           placeholder="选择字典" filterable style="width:140px"
                           @change="(v) => { s.row.optionDictType=''; loadDictTypes(v) }">
                  <el-option v-for="d in dicts" :key="d.code" :label="d.name" :value="d.code"></el-option>
                </el-select>
                <el-select v-if="s.row.optionType==='dict' && s.row.optionDict" v-model="s.row.optionDictType" size="small"
                           clearable filterable placeholder="类型(全部)" style="width:130px;margin-left:4px"
                           @visible-change="(v) => { if(v) loadDictTypes(s.row.optionDict) }">
                  <el-option v-for="t in dictTypesOf(s.row.optionDict)" :key="typeValue(t)"
                             :label="typeLabel(t)" :value="typeValue(t)"></el-option>
                </el-select>
                <el-input v-if="s.row.optionType==='sql'" v-model="s.row.optionSql" size="small" class="mono" placeholder="SELECT code, name FROM ..."></el-input>
              </template>
            </el-table-column>
            <el-table-column label="操作" width="70" fixed="right">
              <template #default="s">
                <el-button size="small" type="danger" link @click="form.params.splice(s.$index,1)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
        </div>

        <!-- ============ 展示列：左侧列清单 + 右侧所选列详情 ============ -->
        <div v-show="tab === 'cols'" class="cfg-pane">
          <div class="cols-layout">
            <!-- 左：列清单 -->
            <div class="cols-list">
              <div class="toolbar">
                <el-button size="small" type="primary" @click="addColumn">添加列</el-button>
                <el-button size="small" type="success" @click="batchHidden(0)">批量启用</el-button>
                <el-button size="small" type="warning" @click="batchHidden(1)">批量关闭</el-button>
                <el-input v-model="colKeyword" size="small" placeholder="搜索列名 / 显示名" clearable
                          style="width:170px;margin-left:auto"></el-input>
                <el-select v-model="colFilter" size="small" style="width:100px;margin-left:4px">
                  <el-option label="全部列" value="all"></el-option>
                  <el-option label="仅已启用" value="on"></el-option>
                  <el-option label="仅未启用" value="off"></el-option>
                </el-select>
              </div>
              <el-table :data="colPaged" border size="small" highlight-current-row
                        :row-class-name="colRowClass" @selection-change="onColSel" @row-click="selectColumn">
                <el-table-column type="selection" width="36"></el-table-column>
                <el-table-column label="序" width="112" align="center">
                  <template #default="s">
                    <span class="drag-handle" draggable="true" title="拖动排序"
                          @dragstart="dragStart(s.row)" @dragover.prevent
                          @drop.prevent="drop(s.row)" @dragend="dragEnd()">⠿</span>
                    <el-input-number :model-value="gi(s.row) + 1" :min="1" :max="form.columns.length"
                                     :controls="false" size="small" style="width:58px"
                                     @change="(v) => { moveTo(s.row, v) }"></el-input-number>
                  </template>
                </el-table-column>
                <el-table-column label="字段名称" min-width="150">
                  <template #default="s"><el-input v-model="s.row.colName" size="small" class="mono"></el-input></template>
                </el-table-column>
                <el-table-column label="字段描述" min-width="130">
                  <template #default="s"><el-input v-model="s.row.label" size="small"></el-input></template>
                </el-table-column>
                <el-table-column label="启用" width="58" align="center">
                  <template #default="s"><el-switch v-model="s.row.hidden" :active-value="0" :inactive-value="1" size="small"></el-switch></template>
                </el-table-column>
                <el-table-column label="查询" width="58" align="center">
                  <template #default="s">
                    <el-switch :model-value="!!colParam(s.row)" @change="toggleColQuery(s.row, $event)" size="small"></el-switch>
                  </template>
                </el-table-column>
                <el-table-column label="操作" width="152" align="center" fixed="right">
                  <template #default="s">
                    <el-button size="small" type="primary" link @click.stop="moveUp(s.row)">上移</el-button>
                    <el-button size="small" type="primary" link @click.stop="moveDown(s.row)">下移</el-button>
                    <el-button size="small" type="danger" link @click.stop="removeColumn(s.row)">删除</el-button>
                  </template>
                </el-table-column>
              </el-table>
              <div class="muted" style="margin:6px 2px 0">
                共 {{ form.columns.length }} 列<template v-if="colFiltered.length !== form.columns.length">，筛选出 {{ colFiltered.length }} 列</template>；点击行在右侧配置详情
              </div>
              <div class="table-footer" v-if="colFiltered.length > 20">
                <el-pagination background size="small" layout="total, sizes, prev, pager, next"
                               :total="colFiltered.length" :current-page="colPage" :page-size="colPageSize"
                               :page-sizes="[20,50,100,200]"
                               @current-change="(p) => { colPage = p }"
                               @size-change="onColSizeChange"></el-pagination>
              </div>
            </div>
            <!-- 右：所选列详情（未选中时不占位，列清单占满整行宽度） -->
            <div class="cols-detail" v-if="selCol && gi(selCol) >= 0">
              <div class="cols-detail-title">
                <span class="mono">{{ selCol.colName || '（未命名列）' }}</span>
                <el-tag size="small" :type="selCol.hidden === 0 ? 'success' : 'info'">{{ selCol.hidden === 0 ? '已启用' : '未启用' }}</el-tag>
              </div>
              <el-collapse v-model="detailOpen">
                <!-- 列显示配置 -->
                <el-collapse-item name="col">
                  <template #title><b>列显示配置</b></template>
                  <el-form label-width="90px" size="small" class="cols-detail-form">
                    <el-form-item label="显示名"><el-input v-model="selCol.label"></el-input></el-form-item>
                    <el-form-item label="列宽度(px)">
                      <el-input-number v-model="selCol.width" :min="0" :controls="false" class="inline-num"></el-input-number>
                      <span class="muted" style="margin-left:8px">0 = 自动</span>
                    </el-form-item>
                    <el-form-item label="格式化">
                      <el-select v-model="selCol.fmtType" clearable style="width:140px">
                        <el-option label="日期" value="date"></el-option>
                        <el-option label="日期时间" value="datetime"></el-option>
                        <el-option label="千分位" value="thousand"></el-option>
                        <el-option label="小数位" value="decimal"></el-option>
                      </el-select>
                      <el-input v-model="selCol.fmtPattern" placeholder="格式参数，如 2" style="width:130px;margin-left:8px"></el-input>
                    </el-form-item>
                    <el-form-item label="字典">
                      <el-select v-model="selCol.dictCode" clearable filterable placeholder="绑定字典" style="width:200px"
                                 @change="(v) => { selCol.dictType=''; if(v) loadDictTypes(v) }">
                        <el-option v-for="d in dicts" :key="d.code" :label="d.name" :value="d.code"></el-option>
                      </el-select>
                    </el-form-item>
                    <el-form-item v-if="selCol.dictCode" label="字典类型">
                      <el-select v-model="selCol.dictType" clearable filterable placeholder="全部" style="width:200px"
                                 @visible-change="(v) => { if(v) loadDictTypes(selCol.dictCode) }">
                        <el-option v-for="t in dictTypesOf(selCol.dictCode)" :key="typeValue(t)"
                                   :label="typeLabel(t)" :value="typeValue(t)"></el-option>
                      </el-select>
                    </el-form-item>
                    <el-form-item v-if="selCol.dictCode" label="列表显示">
                      <el-select v-model="selCol.listFmt" style="width:140px" placeholder="标签">
                        <el-option label="键值" value="value"></el-option>
                        <el-option label="标签" value="label"></el-option>
                        <el-option label="[键值]标签" value="both"></el-option>
                      </el-select>
                      <span class="muted" style="margin-left:12px">导出显示</span>
                      <el-select v-model="selCol.exportFmt" style="width:140px;margin-left:8px" placeholder="标签">
                        <el-option label="键值" value="value"></el-option>
                        <el-option label="标签" value="label"></el-option>
                        <el-option label="[键值]标签" value="both"></el-option>
                      </el-select>
                    </el-form-item>
                  </el-form>
                </el-collapse-item>
                <!-- 查询条件配置 -->
                <el-collapse-item name="query">
                  <template #title><b>搜索项配置</b></template>
                  <template v-if="selParam">
                    <el-form label-width="90px" size="small" class="cols-detail-form">
                      <el-form-item label="参数名">
                        <el-input :model-value="selParam.paramName" class="mono" disabled style="width:160px"></el-input>
                        <span class="muted" style="margin-left:8px">自动装配，SQL 无需写占位符</span>
                      </el-form-item>
                      <el-form-item label="列名">
                        <el-input v-model="selParam.columnName" style="width:200px" placeholder="留空 = 用参数名"></el-input>
                        <span class="muted" style="margin-left:8px">过滤的结果集列名，支持中文</span>
                      </el-form-item>
                      <el-form-item label="显示名"><el-input v-model="selParam.label" style="width:200px"></el-input></el-form-item>
                      <el-form-item label="控件">
                        <el-select v-model="selParam.widget" style="width:160px">
                          <el-option v-for="w in widgets" :key="w.v" :label="w.t" :value="w.v"></el-option>
                        </el-select>
                        <span class="muted" style="margin-left:12px">匹配方式</span>
                        <el-select v-model="selParam.matchType" style="width:110px;margin-left:8px">
                          <el-option label="等于" value="eq"></el-option>
                          <el-option label="模糊" value="like"></el-option>
                          <el-option label="包含" value="in"></el-option>
                          <el-option label="范围" value="range"></el-option>
                        </el-select>
                      </el-form-item>
                      <el-form-item label="必填">
                        <el-switch v-model="selParam.required" :active-value="1" :inactive-value="0"></el-switch>
                        <span class="muted" style="margin-left:24px">默认值</span>
                        <!-- 下拉控件 + 字典来源：默认值直接从字典选项里选；多选控件可选多个 -->
                        <el-select v-if="isDictDefault(selParam)"
                                   :model-value="defaultValModel(selParam)"
                                   :multiple="selParam.widget === 'multiSelect'"
                                   collapse-tags collapse-tags-tooltip
                                   clearable filterable allow-create default-first-option
                                   placeholder="选择字典选项" style="width:180px;margin-left:8px"
                                   @change="(v) => setDefaultVal(selParam, v)"
                                   @visible-change="(v) => { if (v) loadDictOptions(selParam) }">
                          <el-option v-for="o in dictOptionsOf(selParam)" :key="String(o.value)"
                                     :label="o.label" :value="String(o.value)"></el-option>
                        </el-select>
                        <el-input v-else v-model="selParam.defaultVal" style="width:130px;margin-left:8px"
                                  :disabled="!!selParam.defaultMacro"></el-input>
                        <el-select v-model="selParam.defaultMacro" style="width:110px;margin-left:8px" clearable placeholder="动态宏">
                          <el-option label="今天" value="today"></el-option>
                          <el-option label="昨天" value="yesterday"></el-option>
                          <el-option label="近7天" value="last7days"></el-option>
                          <el-option label="近30天" value="last30days"></el-option>
                          <el-option label="本月" value="thisMonth"></el-option>
                        </el-select>
                      </el-form-item>
                      <el-form-item label="选项来源">
                        <el-radio-group v-model="selParam.optionType">
                          <el-radio value="fixed">固定</el-radio>
                          <el-radio value="dict">字典</el-radio>
                          <el-radio value="sql">SQL</el-radio>
                        </el-radio-group>
                        <span class="muted" style="margin-left:12px">下拉框 / 单选组的候选值来源</span>
                      </el-form-item>
                      <el-form-item v-if="selParam.optionType === 'fixed'" label="固定选项">
                        <el-input v-model="selParam.optionFixed" class="mono" type="textarea" :rows="2"
                                  placeholder='[{"value":1,"label":"启用"}]'></el-input>
                      </el-form-item>
                      <el-form-item v-if="selParam.optionType === 'dict'" label="选择字典">
                        <el-select v-model="selParam.optionDict" filterable placeholder="选择字典" style="width:220px"
                                   @change="(v) => { selParam.optionDictType = ''; loadDictTypes(v) }">
                          <el-option v-for="d in dicts" :key="d.code"
                                     :label="d.name + '（' + d.code + '）'" :value="d.code"></el-option>
                        </el-select>
                      </el-form-item>
                      <el-form-item v-if="selParam.optionType === 'dict'" label="字典类型">
                        <el-select v-model="selParam.optionDictType" clearable filterable placeholder="全部类型"
                                   style="width:220px"
                                   @visible-change="(v) => { if (v) loadDictTypes(selParam.optionDict) }">
                          <el-option v-for="t in dictTypesOf(selParam.optionDict)" :key="typeValue(t)"
                                     :label="typeLabel(t)" :value="typeValue(t)"></el-option>
                        </el-select>
                        <span class="muted" style="margin-left:8px">字典在「字典管理」页维护，可复用</span>
                      </el-form-item>
                      <el-form-item v-if="selParam.optionType === 'sql'" label="选项SQL">
                        <el-input v-model="selParam.optionSql" class="mono" type="textarea" :rows="2"
                                  placeholder="SELECT code, name FROM ..."></el-input>
                      </el-form-item>
                    </el-form>
                    <div class="muted" style="margin-left:90px">
                      与「查询条件」步骤是同一份数据，两边改任一处都实时同步
                    </div>
                  </template>
                  <div v-else style="margin:4px 0 8px 12px">
                    <span class="muted">该列未开启查询。开启后自动装配为查询条件。</span>
                    <el-button size="small" type="primary" style="margin-left:12px"
                               @click="() => { toggleColQuery(selCol, true) }">开启查询</el-button>
                  </div>
                </el-collapse-item>
              </el-collapse>
            </div>
          </div>
        </div>

        <!-- ============ 权限 ============ -->
        <div v-show="tab === 'perm'" class="cfg-pane">
          <el-form label-width="110px" style="max-width:520px">
            <el-form-item label="可见角色">
              <el-select v-model="form.roleCodes" multiple style="width:300px">
                <el-option v-for="r in roleOptions" :key="r" :label="roleLabel(r)" :value="r"></el-option>
              </el-select>
              <div class="muted">普通用户仅能查看「已发布 + 已授权」的报表</div>
            </el-form-item>
          </el-form>
        </div>
      </section>
    </div>

      <!-- 预览结果 -->
      <el-dialog v-model="previewVisible" title="预览（前 100 行）" width="90%" top="4vh">
        <el-table :data="previewRows" border size="small" max-height="60vh" v-loading="previewing">
          <el-table-column v-for="(c,i) in previewCols" :key="c.name"
                           :prop="'c'+i" :label="c.label || c.name" min-width="120" class-name="mono">
            <template #default="s">{{ s.row['c'+i] === null ? '' : s.row['c'+i] }}</template>
          </el-table-column>
        </el-table>
        <div class="toolbar" style="margin-top:10px">
          <el-button size="small" @click="fillColumnsFromPreview">用预览结果填充展示列</el-button>
          <span class="muted">共 {{ previewData.total }} 行（仅展示前 100 行）</span>
          <span class="muted" v-if="(previewData.columns || []).length > 40">
            共 {{ (previewData.columns || []).length }} 列，预览仅渲染前 40 列（「填充展示列」仍按全部列加载）
          </span>
        </div>
      </el-dialog>

      <!-- 公开访问 -->
      <el-dialog v-model="publicVisible" title="公开访问（无需登录）" width="560px">
        <template v-if="report.isPublic===1">
          <el-alert type="warning" :closable="false" show-icon
                    title="公开访问已开启。链接与令牌仅在生成时完整展示一次，请妥善保管"></el-alert>
          <div style="margin-top:12px;display:flex;align-items:center;justify-content:space-between">
            <div>
              <span style="margin-right:8px">允许公开导出</span>
              <el-switch v-model="report.pubAllowExport" @change="togglePublicExport"></el-switch>
              <span class="muted" style="margin-left:8px">开启后公开页右上角出现导出按钮（勾选行/当前页/指定页/全部）</span>
            </div>
          </div>
          <div style="margin-top:12px">
            <el-button type="danger" @click="disablePublic">关闭公开访问</el-button>
          </div>
        </template>
        <template v-else>
          <el-alert type="info" :closable="false" show-icon
                    title="开启后将生成公开链接，任何人可通过链接查看该报表（匿名不可见 SQL 与数据源信息）"></el-alert>
          <div style="margin-top:12px">
            <el-button type="primary" :loading="publicLoading" @click="enablePublic">生成公开链接</el-button>
          </div>
        </template>
        <div v-if="publicLink" style="margin-top:14px">
          <el-input :model-value="publicLink" readonly>
            <template #append><el-button @click="copyLink">复制</el-button></template>
          </el-input>
          <div class="muted" style="margin-top:6px">⚠️ 令牌仅显示这一次，关闭后需重新生成</div>
        </div>
      </el-dialog>

      <!-- 表索引浏览（只读）：判断排序/过滤字段是否走索引，配合慢查询排查 -->
      <el-dialog v-model="idx.visible" :title="'表索引：' + (pick.table || '')" width="640px">
        <div v-if="idx.loading" class="muted">加载中…</div>
        <div v-else-if="!idx.list.length" class="muted">
          该表没有索引，或当前数据库类型不支持索引浏览
        </div>
        <el-table v-else :data="idx.list" size="small" border max-height="420">
          <el-table-column prop="name" label="索引名" min-width="200" show-overflow-tooltip />
          <el-table-column label="类型" width="96">
            <template #default="{ row }">
              <el-tag v-if="row.primary" type="danger" size="small">主键</el-tag>
              <el-tag v-else-if="row.unique" type="warning" size="small">唯一</el-tag>
              <el-tag v-else type="info" size="small">普通</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="索引列（按顺序）" min-width="260">
            <template #default="{ row }">
              <span class="mono">{{ (row.columns || []).join(', ') }}</span>
            </template>
          </el-table-column>
        </el-table>
        <div class="muted" style="margin-top:10px">
          索引仅用于参考：查询条件里对索引列做函数运算（如 <span class="mono">TO_CHAR(dt,'YYYY')</span>）会导致索引失效
        </div>
      </el-dialog>
  </div>
</template>

<script>
import { ElMessageBox, ElMessage } from 'element-plus'
import { ArrowLeft } from '@element-plus/icons-vue'
import { RS } from '@/lib/rs.js'
import SqlEditor from '@/components/SqlEditor.vue'

/** 共享空数组：字典类型未加载时的占位，避免每次渲染都新建数组对象触发无谓更新 */
var EMPTY_TYPES = [];
/** 进行中的字典类型请求（不放进 data：Promise 被 Vue 代理后会破坏 then 调用） */
var DICT_TYPE_PENDING = {};

export default {
  components: { SqlEditor, ArrowLeft },
  data: function () {
    return {
      id: 0, tab: 'base', loading: false,
      form: this.emptyForm(),
      // 报表状态/公开标记的 UI 状态（模板中使用，必须先初始化避免渲染报错）
      report: { status: '', isPublic: 0, pubAllowExport: false },
      groups: [], datasources: [], roleOptions: [], roleNames: {}, dicts: [],
      widgets: [
        { v: 'text', t: '文本' }, { v: 'number', t: '数字' }, { v: 'select', t: '下拉单选' },
        { v: 'multiSelect', t: '下拉多选' }, { v: 'date', t: '日期' },
        { v: 'dateTime', t: '日期时间' }, { v: 'dateRange', t: '日期范围' }
      ],
      saving: false, previewing: false, previewVisible: false, previewData: {},
      publicVisible: false, publicLoading: false, publicLink: '',
      colSel: [], dragIdx: -1,
      // 取数方式：sql=手写 SQL / table=选数据源→选表→勾字段后自动生成 SQL
      // tables 为当前关键字命中的表清单（后端缓存后内存过滤，total/truncated 用于提示截断）
      pick: {
        mode: 'sql', table: '', cols: [], tables: [], columns: [],
        loadingTables: false, total: 0, truncated: false, ageSec: 0, timer: 0
      },
      // 表索引浏览（只读）
      idx: { visible: false, loading: false, list: [] },
      // 展示列分页/筛选：列数上百时只渲染当前页，避免一次性挂载上千个表单控件
      colPage: 1, colPageSize: 20, colKeyword: '', colFilter: 'all',
      // 右侧详情面板：当前选中列 + 折叠面板展开项
      selCol: null, detailOpen: ['col', 'query'],
      dictTypeCache: {}, dictOptCache: {}
    };
  },
  computed: {
    /** 表清单的缓存年龄文案：让用户知道看到的是「刚扫的」还是「几分钟前的快照」 */
    cacheAgoText: function () {
      var s = this.pick.ageSec || 0;
      if (s <= 0) return '';
      if (s < 60) return '缓存 ' + s + ' 秒前';
      if (s < 3600) return '缓存 ' + Math.round(s / 60) + ' 分钟前';
      return '缓存 ' + Math.round(s / 3600) + ' 小时前';
    },
    /** 参数名 → 条件项 索引（O(1) 查找，避免每行渲染线性扫描 params） */
    paramMap: function () {
      var m = {};
      (this.form.params || []).forEach(function (p) { if (p.paramName) m[p.paramName] = p; });
      return m;
    },
    /** 展示列：按关键字/启用状态筛选 */
    colFiltered: function () {
      var kw = (this.colKeyword || '').trim().toLowerCase();
      var f = this.colFilter;
      return (this.form.columns || []).filter(function (c) {
        var hidden = c.hidden === 1 ? 1 : 0; // 归一化：仅 1 视为未启用
        if (f === 'on' && hidden === 1) return false;
        if (f === 'off' && hidden === 0) return false;
        if (kw) {
          var t = ((c.colName || '') + ' ' + (c.label || '')).toLowerCase();
          if (t.indexOf(kw) < 0) return false;
        }
        return true;
      });
    },
    /** 展示列：当前页数据（页码超出范围时自动收敛，不会出现空白页） */
    colPaged: function () {
      var list = this.colFiltered;
      var size = this.colPageSize;
      var maxPage = Math.max(1, Math.ceil(list.length / size));
      var p = Math.min(Math.max(1, this.colPage), maxPage);
      return list.slice((p - 1) * size, p * size);
    },
    /** 预览行数据：后端返回的是二维数组，转成 c0/c1/c2... 键供表格渲染 */
    previewRows: function () {
      return (this.previewData.rows || []).map(function (r) {
        var o = {};
        r.forEach(function (v, i) { o['c' + i] = v; });
        return o;
      });
    },
    /** 预览列：宽表下只渲染前 40 列，避免几百列 × 100 行把浏览器卡死 */
    previewCols: function () {
      return (this.previewData.columns || []).slice(0, 40);
    },
    /** 右侧详情面板对应的查询条件（所选列开启查询时才有） */
    selParam: function () {
      return this.selCol ? this.colParam(this.selCol) : undefined;
    },
    /** 左侧步骤导航：数量徽标跟随配置实时变化，不用来回切页签确认 */
    steps: function () {
      return [
        { name: 'base', idx: 1, label: '基础信息', count: null },
        { name: 'params', idx: 2, label: '查询条件', count: (this.form.params || []).length },
        { name: 'cols', idx: 3, label: '展示列', count: (this.form.columns || []).length },
        { name: 'perm', idx: 4, label: '权限', count: null }
      ];
    },
    statusText: function () {
      var s = this.report.status;
      if (s === 'published') return '已发布';
      if (s === 'disabled') return '已停用';
      return '草稿';
    },
    statusType: function () {
      var s = this.report.status;
      if (s === 'published') return 'success';
      if (s === 'disabled') return 'info';
      return 'warning';
    },
    /** 已配置的条件参数名（SQL 补全与「插入参数」按钮用） */
    paramNames: function () {
      var out = [];
      (this.form.params || []).forEach(function (p) { if (p.paramName) out.push(p.paramName); });
      return out;
    },
    /** 补全用的纯表名 / 纯字段名（编辑器只认字符串数组） */
    tableNames: function () {
      return (this.pick.tables || []).map(function (t) { return t.name; });
    },
    tableColumnNames: function () {
      return (this.pick.columns || []).map(function (c) { return c.name; });
    },
    allPicked: function () {
      return this.pick.columns.length > 0 && this.pick.cols.length === this.pick.columns.length;
    },
    somePicked: function () {
      return this.pick.cols.length > 0 && this.pick.cols.length < this.pick.columns.length;
    }
  },
  watch: {
    /** 当前页变化时按需预热字典类型清单（仅可见行引用的字典，去重 + 限并发） */
    colPaged: function (rows) { this.warmDictTypes(rows); },
    /** 选中列引用了字典时按需预热其类型清单 */
    selCol: function (col) { if (col) this.warmDictTypes([col]); }
  },
  created: function () {
    var self = this;
    if (!RS.requireLogin()) return;
    this.id = parseInt(RS.qs('id'), 10) || 0;
    // 新建报表需 report:create，编辑已有报表需 report:update
    RS.guard(this.id ? 'report:update' : 'report:create').then(function () {
      self.loadRefs();
      if (self.id) self.load();
    }).catch(function () {});
  },
  methods: {
    emptyForm: function () {
      return {
        name: '', groupId: null, datasourceId: null, sqlText: '',
        queryTimeout: 30, rowLimit: 100000, exportLimit: 5000000,
        defaultOrder: '', remark: '',
        params: [], columns: [], roleCodes: []
      };
    },
    emptyParam: function () {
      return { paramName: '', columnName: '', label: '', widget: 'text', matchType: 'eq', required: 0,
        defaultVal: '', defaultMacro: '', optionType: 'fixed', optionFixed: '', optionSql: '',
        optionDict: '', optionDictType: '', sortNo: 0 };
    },
    emptyColumn: function () {
      // 新列默认未启用（hidden=1），由用户手动/批量启用
      return { colName: '', label: '', width: 0, hidden: 1, fmtType: '', fmtPattern: '',
        dictCode: '', dictType: '', listFmt: '', exportFmt: '', sortNo: 0 };
    },
    /** 返回报表中心（配置页不再挂左侧菜单，统一从这里进出） */
    back: function () {
      this.$router.push('/reports').catch(function () {});
    },
    /** 切换取数方式：切到「选表」且已选数据源时，顺手把表清单拉出来 */
    onModeChange: function (mode) {
      if (mode === 'table' && this.form.datasourceId && !this.pick.tables.length) this.loadTables('');
    },
    /**
     * 换数据源：清空表/字段选择。
     * 必须清——否则补全里会留着上一个库的表名，选出来的字段也不属于当前库。
     */
    onDatasourceChange: function () {
      this.pick.table = '';
      this.pick.tables = [];
      this.pick.columns = [];
      this.pick.cols = [];
      if (this.pick.mode === 'table') this.loadTables('');
    },
    /**
     * 拉取表清单（含视图）。
     * 后端对表清单做了 TTL 缓存：keyword 由后端在缓存的全量清单上内存过滤，
     * 所以这里可以放心逐字搜索（配合 250ms 防抖，避免连续输入产生过多请求）。
     * @param {string} kw 表名关键字，空串取全部
     * @param {boolean} force 是否强制重扫业务库（「刷新表清单」按钮）
     */
    loadTables: function (kw, force) {
      var self = this;
      if (!this.form.datasourceId) { this.pick.loadingTables = false; return; }
      this.pick.loadingTables = true;
      var qs = [];
      if (kw) qs.push('keyword=' + encodeURIComponent(kw));
      if (force) qs.push('refresh=1');
      var url = '/api/datasources/' + this.form.datasourceId + '/tables' + (qs.length ? '?' + qs.join('&') : '');
      RS.get(url)
        .then(function (res) {
          self.pick.tables = (res && res.items) || [];
          self.pick.total = (res && res.total) || self.pick.tables.length;
          self.pick.truncated = !!(res && res.truncated);
          self.pick.ageSec = (res && res.ageSec) || 0;
        })
        .catch(function (e) {
          self.pick.tables = [];
          // 没有「数据源查看」权限时浏览表结构会被拦，提示换成手写 SQL 比抛原始 403 有用
          if (String((e && e.message) || '').indexOf('权限') >= 0) {
            ElMessage.warning('当前账号没有浏览表结构的权限，请改用「手写 SQL」');
          } else {
            RS.alertErr(e);
          }
        })
        .finally(function () { self.pick.loadingTables = false; });
    },
    /** 表下拉的远程搜索（el-select filterable remote）：250ms 防抖 */
    searchTables: function (kw) {
      var self = this;
      clearTimeout(this.pick.timer);
      this.pick.timer = setTimeout(function () { self.loadTables(kw || ''); }, 250);
    },
    /** 选中表后加载字段，默认全选（大多数报表要的就是全字段） */
    onTableChange: function (name) {
      var self = this;
      this.pick.columns = [];
      this.pick.cols = [];
      this.idx.list = [];
      if (!name) return;
      RS.get('/api/datasources/' + this.form.datasourceId + '/tables/' +
        encodeURIComponent(name) + '/columns')
        .then(function (res) {
          self.pick.columns = (res && res.items) || [];
          self.pick.cols = self.pick.columns.map(function (c) { return c.name; });
        })
        .catch(function (e) { RS.alertErr(e); });
    },
    /** 打开当前表的索引清单（只读，用于判断排序字段是否走索引） */
    openIndexes: function () {
      var self = this;
      if (!this.pick.table) { ElMessage.warning('请先选择数据表'); return; }
      this.idx.visible = true;
      this.idx.loading = true;
      RS.get('/api/datasources/' + this.form.datasourceId + '/tables/' +
        encodeURIComponent(this.pick.table) + '/indexes')
        .then(function (list) { self.idx.list = list || []; })
        .catch(function (e) { self.idx.list = []; RS.alertErr(e); })
        .finally(function () { self.idx.loading = false; });
    },
    /** 单个字段勾选/取消 */
    toggleCol: function (name, on) {
      var i = this.pick.cols.indexOf(name);
      if (on && i < 0) this.pick.cols.push(name);
      if (!on && i >= 0) this.pick.cols.splice(i, 1);
    },
    /** 全选 / 全不选 */
    toggleAllCols: function (on) {
      this.pick.cols = on
        ? this.pick.columns.map(function (c) { return c.name; })
        : [];
    },
    /**
     * 选表结果落地：生成 SELECT 语句 + 同步填充展示列。
     * 优先调用后端统一生成器（引号处理、保留字、跨模式表名等规则只有一份）；
     * 接口不可用时回退到本地拼串，保证配置页在离线/权限受限时仍可用。
     */
    applyPick: function () {
      if (!this.pick.table || !this.pick.cols.length) {
        ElMessage.warning('请先选择数据表和输出字段');
        return;
      }
      var self = this;
      var cols = this.pick.cols.slice();
      RS.post('/api/report-wizard/generate', {
        datasourceId: this.form.datasourceId,
        table: this.pick.table,
        columns: cols.map(function (n) { return { name: n }; }),
        withParams: false
      })
        .then(function (res) {
          self.form.sqlText = res.sqlText;
          var added = self.mergeColumns((res.columns || []).map(function (c) {
            return { name: c.colName, label: c.label };
          }));
          ElMessage.success('已生成 SQL' +
            (added ? '，并填充 ' + added + ' 个展示列' : '（展示列已存在，未重复添加）'));
        })
        .catch(function () { self.applyPickLocal(); });
    },
    /** 本地兜底：接口不可用时按简单规则拼 SQL（不含引号/保留字处理） */
    applyPickLocal: function () {
      var table = this.pick.table;
      var cols = this.pick.cols.slice();
      this.form.sqlText = cols.length > 6
        ? 'SELECT\n  ' + cols.join(',\n  ') + '\nFROM ' + table
        : 'SELECT ' + cols.join(', ') + '\nFROM ' + table;
      var meta = {};
      this.pick.columns.forEach(function (c) { meta[c.name] = c; });
      var added = this.mergeColumns(cols.map(function (n) {
        var m = meta[n] || {};
        return { name: n, label: (m.comment || '').trim() || n }; // 显示名优先取字段注释；Oracle 驱动把无注释 COALESCE 成空格，必须 trim 后再回退字段名
      }));
      ElMessage.success('已生成 SQL' +
        (added ? '，并填充 ' + added + ' 个展示列' : '（展示列已存在，未重复添加）'));
    },
    /**
     * 把 [{name,label}] 合并进展示列：已存在的列名跳过，不清除既有配置
     * @param {Array<{name:string,label:string}>} list 待合并列
     * @returns {number} 实际新增的列数
     */
    mergeColumns: function (list) {
      var self = this;
      var existing = {};
      this.form.columns.forEach(function (x) { if (x.colName) existing[x.colName] = 1; });
      var added = 0;
      (list || []).forEach(function (c) {
        if (!c || !c.name || existing[c.name]) return;
        var col = self.emptyColumn();
        col.colName = c.name;
        col.label = c.label || c.name;
        self.form.columns.push(col);
        existing[c.name] = 1;
        added += 1;
      });
      return added;
    },
    /** 在 SQL 光标处插入条件参数占位符 */
    insertParam: function (name) {
      var ed = this.$refs.sqlEditor;
      if (ed && ed.insertText) ed.insertText(':' + name);
    },
    loadRefs: function () {
      var self = this;
      RS.get('/api/report-groups').then(function (g) { self.groups = g; }).catch(function () {});
      RS.get('/api/datasources').then(function (d) { self.datasources = d; }).catch(function () {});
      RS.get('/api/dictionaries').then(function (l) { self.dicts = l || []; }).catch(function () {});
      RS.get('/api/roles').then(function (roles) {
        // /api/roles 返回启用中的角色 [{roleKey, roleName}]
        var keys = [], names = {};
        (roles || []).forEach(function (r) {
          keys.push(r.roleKey);
          names[r.roleKey] = r.roleName;
        });
        self.roleOptions = keys;
        self.roleNames = names;
        // 新建报表默认授予全部启用角色，避免保存后无人可见
        if (!self.id && (!self.form.roleCodes || !self.form.roleCodes.length)) self.form.roleCodes = keys.slice();
      }).catch(function () {});
    },
    roleLabel: function (r) {
      return this.roleNames[r] ? this.roleNames[r] + '（' + r + '）' : r;
    },
    load: function () {
      var self = this;
      this.loading = true;
      RS.get('/api/reports/' + this.id).then(function (data) {
        self.form = Object.assign(self.emptyForm(), data.report, {
          params: data.params || [], columns: data.columns || [],
          roleCodes: data.roleCodes || []
        });
        // 已有授权回显数据库真实值（为空即表示未授权给任何角色）
        if (!self.form.roleCodes) self.form.roleCodes = [];
        // 同步公开访问状态（公开访问对话框依赖 report.isPublic）
        self.report.status = data.report.status || '';
        self.report.isPublic = data.report.isPublic || 0;
        self.report.pubAllowExport = data.report.pubAllowExport === 1;
        // 只预热当前页可见列引用的字典类型（去重 + 限并发），列多时不再一次打出上百个请求
        self.warmDictTypes(self.colPaged);
        (self.form.params || []).forEach(function (p) {
          if (p.optionType === 'dict' && p.optionDict) self.loadDictTypes(p.optionDict);
        });
      }).catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.loading = false; });
    },
    validate: function () {
      var f = this.form;
      if (!f.name) { this.tab = 'base'; return '请填写报表名称'; }
      if (!f.groupId) { this.tab = 'base'; return '请选择分组'; }
      if (!f.datasourceId) { this.tab = 'base'; return '请选择数据源'; }
      if (!f.sqlText.trim()) { this.tab = 'base'; return '请填写查询 SQL'; }
      for (var i = 0; i < f.params.length; i++) {
        var p = f.params[i];
        if (!p.paramName || !p.label) { this.tab = 'params'; return '条件参数名与显示名必填'; }
        if (p.optionType === 'fixed' && p.optionFixed) {
          try { JSON.parse(p.optionFixed); } catch (e) { this.tab = 'params'; return '条件「' + p.label + '」固定选项不是合法 JSON'; }
        }
      }
      for (var j = 0; j < f.columns.length; j++) {
        if (!f.columns[j].colName) { this.tab = 'cols'; return '展示列名必填'; }
      }
      return null;
    },
    save: function () {
      var self = this;
      var err = this.validate();
      if (err) { ElMessage.warning(err); return; }
      // 保存前重排：启用列在前（保持原相对顺序），未启用列统一排到末尾，序号随之重编
      var enabled = [], disabled = [];
      this.form.columns.forEach(function (c) { (c.hidden === 1 ? disabled : enabled).push(c); });
      var moved = disabled.length > 0 && enabled.length > 0;
      if (moved) this.form.columns = enabled.concat(disabled);
      this.saving = true;
      var f = JSON.parse(JSON.stringify(this.form));
      f.params.forEach(function (p, i) { p.sortNo = i; });
      f.columns.forEach(function (c, i) { c.sortNo = i; });
      var p = this.id ? RS.put('/api/reports/' + this.id, f) : RS.post('/api/reports', f);
      p.then(function (data) {
        ElMessage.success('已保存' + (moved ? '，未启用列已排至末尾' : ''));
        // 新建保存后补上 id：先回写 self.id（同路由 replace 不会重建组件，created 不会重跑），
        // 否则再次保存会走 POST 重复创建；用 replace 而不是 push，避免浏览器后退又回到「新建」空表单
        self.id = data.id;
        if (!self.$route.query.id) self.$router.replace({ path: '/config', query: { id: data.id } });
      }).catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.saving = false; });
    },
    preview: function () {
      var self = this;
      this.previewing = true;
      this.previewVisible = true;
      RS.post('/api/reports/' + this.id + '/preview', {})
        .then(function (data) {
          self.previewData = data || {};
          self.fillColumns(false); // 预览成功后自动加载查询的全部列（显示名默认取字段注释）
        })
        .catch(function (e) { self.previewVisible = false; RS.alertErr(e); })
        .finally(function () { self.previewing = false; });
    },
    /**
     * 把预览结果列填充进展示列（已存在的跳过，不清除既有配置）
     * @param {boolean} switchTab 填充后是否跳到「展示列」步骤
     */
    fillColumns: function (switchTab) {
      var added = this.mergeColumns((this.previewData.columns || []).map(function (c) {
        return { name: c.name, label: c.label || c.name }; // label 由后端预填字段注释
      }));
      ElMessage.success(added ? '已加载 ' + added + ' 列（默认未启用，请勾选后批量启用）' : '展示列已全部加载');
      if (switchTab) {
        this.tab = 'cols';
        // 新列追加在末尾，清空筛选并跳到包含新列的最后一页
        if (added) {
          this.colKeyword = '';
          this.colFilter = 'all';
          this.colPage = Math.max(1, Math.ceil(this.form.columns.length / this.colPageSize));
        }
      }
    },
    fillColumnsFromPreview: function () { this.fillColumns(true); },
    onColSel: function (rows) { this.colSel = rows; },
    batchHidden: function (hidden) {
      // 无勾选时作用于全部列（含未在当前页显示的列）
      var rows = this.colSel && this.colSel.length ? this.colSel : this.form.columns;
      if (!rows.length) { ElMessage.warning('暂无可操作的列，请先预览查询'); return; }
      rows.forEach(function (r) { r.hidden = hidden; });
      ElMessage.success((hidden === 0 ? '已启用 ' : '已关闭 ') + rows.length + ' 列' +
        (this.colSel && this.colSel.length ? '' : '（全部）'));
    },
    /** 行在完整 columns 数组中的下标（分页/筛选后仍能定位真实位置） */
    gi: function (row) { return this.form.columns.indexOf(row); },
    /** 删除列（按行引用定位，兼容分页/筛选视图）；若删除的是选中列，同步清空右侧详情 */
    removeColumn: function (row) {
      var i = this.gi(row);
      if (i < 0) return;
      if (this.selCol === row) this.selCol = null;
      this.form.columns.splice(i, 1);
    },
    /**
     * 把某列移动到指定序号位置（1 起）
     * @param {Object} row 展示列对象
     * @param {number} target 目标序号
     */
    moveTo: function (row, target) {
      var arr = this.form.columns;
      var from = arr.indexOf(row);
      var to = parseInt(target, 10) - 1;
      if (from < 0 || isNaN(to)) return;
      to = Math.min(Math.max(0, to), arr.length - 1);
      if (to === from) return;
      arr.splice(to, 0, arr.splice(from, 1)[0]);
    },
    /**
     * 拖动排序起点：记录被拖行在目标数组中的下标
     * @param {Object} row 被拖行
     * @param {Array} [arr] 目标数组，缺省为展示列数组
     */
    dragStart: function (row, arr) { this.dragIdx = (arr || this.form.columns).indexOf(row); },
    dragEnd: function () { this.dragIdx = -1; },
    /**
     * 拖动落点：把被拖行移动到目标行位置
     * @param {Object} row 落点行
     * @param {Array} [arr] 目标数组，缺省为展示列数组
     */
    drop: function (row, arr) {
      var list = arr || this.form.columns;
      var from = this.dragIdx;
      var to = list.indexOf(row);
      this.dragIdx = -1;
      if (from < 0 || to < 0 || from === to) return;
      list.splice(to, 0, list.splice(from, 1)[0]);
    },
    /** 上移一列（已在首位时忽略） */
    moveUp: function (row) {
      var i = this.form.columns.indexOf(row);
      if (i > 0) {
        this.form.columns.splice(i - 1, 0, this.form.columns.splice(i, 1)[0]);
      }
    },
    /** 下移一列（已在末位时忽略） */
    moveDown: function (row) {
      var arr = this.form.columns;
      var i = arr.indexOf(row);
      if (i >= 0 && i < arr.length - 1) {
        arr.splice(i + 1, 0, arr.splice(i, 1)[0]);
      }
    },
    /** 行点击 → 选中该列，右侧面板展示其详情（点击开关等控件时不触发） */
    selectColumn: function (row, column, event) {
      var t = event && event.target;
      if (t && t.closest && t.closest('input,textarea,select,button,label,.el-switch,.el-input-number,.el-select,.el-checkbox,.drag-handle')) return;
      this.selCol = (this.selCol === row) ? null : row; // 再点一次取消选中
    },
    /** 选中行高亮（row-class-name 回调） */
    colRowClass: function (o) { return o.row === this.selCol ? 'col-selected' : ''; },
    onColSizeChange: function (n) { this.colPageSize = n; this.colPage = 1; },
    setStatus: function (status) {
      var self = this;
      RS.post('/api/reports/' + this.id + '/publish', { status: status })
        .then(function () { ElMessage.success('已' + (status === 'published' ? '发布' : '停用')); self.load(); })
        .catch(function (e) { RS.alertErr(e); });
    },
    addGroup: function () {
      var self = this;
      ElMessageBox.prompt('请输入分组名称', '新建分组')
        .then(function (r) { return RS.post('/api/report-groups', { name: r.value }); })
        .then(function (g) { self.groups.push(g); self.form.groupId = g.id; })
        .catch(function (e) { if (e !== 'cancel' && e !== 'close') RS.alertErr(e); });
    },
    enablePublic: function () {
      var self = this;
      this.publicLoading = true;
      RS.post('/api/reports/' + this.id + '/public')
        .then(function (data) {
          self.report.isPublic = 1;
          self.publicLink = location.origin + data.previewLink;
        })
        .catch(function (e) { RS.alertErr(e); })
        .finally(function () { self.publicLoading = false; });
    },
    disablePublic: function () {
      var self = this;
      ElMessageBox.confirm('关闭后原公开链接将立即失效', '确认')
        .then(function () { return RS.del('/api/reports/' + self.id + '/public'); })
        .then(function () { self.report.isPublic = 0; self.publicLink = ''; ElMessage.success('已关闭'); })
        .catch(function (e) { if (e !== 'cancel' && e !== 'close') RS.alertErr(e); });
    },
    /** 切换「允许公开导出」开关；失败时回滚 UI 状态 */
    togglePublicExport: function (val) {
      var self = this;
      RS.put('/api/reports/' + this.id + '/public', { allowExport: !!val })
        .then(function () {
          ElMessage.success(val ? '已允许公开导出' : '已禁止公开导出');
        })
        .catch(function (e) {
          self.report.pubAllowExport = !val; // 保存失败回滚开关
          RS.alertErr(e);
        });
    },
    copyLink: function () {
      var self = this;
      navigator.clipboard.writeText(this.publicLink).then(function () {
        ElMessage.success('已复制');
      }).catch(function () { RS.alertErr(new Error('复制失败，请手动选择复制')); });
    },
    addParam: function () { this.form.params.push(this.emptyParam()); },
    addColumn: function () {
      var c = this.emptyColumn();
      this.form.columns.push(c);
      this.selCol = c; // 新列直接选中，右侧面板立即可填
    },
    /**
     * 加载字典的可用「字典类型」清单（按编码缓存；进行中的请求复用，避免重复打点）
     * @param {string} code 字典编码
     * @returns {Promise<Array>} 类型清单
     */
    loadDictTypes: function (code) {
      if (!code) return Promise.resolve(EMPTY_TYPES);
      if (this.dictTypeCache[code]) return Promise.resolve(this.dictTypeCache[code]);
      if (DICT_TYPE_PENDING[code]) return DICT_TYPE_PENDING[code];
      var self = this;
      var d = this.dicts.find(function (x) { return x.code === code; });
      if (!d) return Promise.resolve(EMPTY_TYPES);
      var p = RS.get('/api/dictionaries/' + d.id + '/types').then(function (types) {
        self.dictTypeCache[code] = types || EMPTY_TYPES;
        delete DICT_TYPE_PENDING[code];
        return self.dictTypeCache[code];
      }).catch(function () {
        self.dictTypeCache[code] = EMPTY_TYPES;
        delete DICT_TYPE_PENDING[code];
        return EMPTY_TYPES;
      });
      DICT_TYPE_PENDING[code] = p;
      return p;
    },
    /** 字典类型清单（未加载时返回共享空数组，避免每次渲染新建数组对象） */
    dictTypesOf: function (code) {
      return (code && this.dictTypeCache[code]) || EMPTY_TYPES;
    },
    /**
     * 按需预热若干列引用的字典类型清单（去重 + 并发上限 3）
     * @param {Array} rows 展示列行
     */
    warmDictTypes: function (rows) {
      var self = this;
      var codes = [];
      (rows || []).forEach(function (c) {
        var code = c && c.dictCode;
        if (!code) return;
        if (self.dictTypeCache[code] || DICT_TYPE_PENDING[code]) return;
        if (codes.indexOf(code) < 0) codes.push(code);
      });
      if (!codes.length) return;
      var idx = 0;
      var next = function () {
        if (idx >= codes.length) return;
        var code = codes[idx++];
        self.loadDictTypes(code).then(next, next);
      };
      for (var k = 0; k < 3 && k < codes.length; k++) next();
    },
    /** 类型下拉显示文本：{value,label} 显示 名称（编码），纯字符串直接显示（兼容旧缓存） */
    typeLabel: function (t) {
      if (t && typeof t === 'object') {
        var v = String(t.value == null ? '' : t.value);
        var l = t.label == null ? '' : String(t.label);
        return (l && l !== v) ? l + '（' + v + '）' : v;
      }
      return String(t);
    },
    /** 类型下拉选中值：取类型编码字符串 */
    typeValue: function (t) {
      return (t && typeof t === 'object') ? String(t.value) : String(t);
    },
    /** 该条件是否用字典选项做默认值下拉：下拉类控件 + 字典来源 + 已绑定字典编码 */
    isDictDefault: function (p) {
      return (p.widget === 'select' || p.widget === 'multiSelect') &&
        p.optionType === 'dict' && !!p.optionDict;
    },
    /**
     * 默认值下拉的绑定值：多选控件需数组，把逗号分隔字符串拆开（与报表页回显口径一致）
     * @param {Object} p 查询条件
     */
    defaultValModel: function (p) {
      if (p.widget !== 'multiSelect') return p.defaultVal;
      return (p.defaultVal || '') === '' ? [] : String(p.defaultVal).split(',');
    },
    /**
     * 默认值下拉写回：多选控件把数组用逗号拼回字符串存库
     * @param {Object} p 查询条件
     * @param {string|Array} v 下拉新值
     */
    setDefaultVal: function (p, v) {
      p.defaultVal = Array.isArray(v) ? v.join(',') : v;
    },
    /** 字典选项缓存键：编码 + 类型（类型不同选项集不同） */
    dictOptKey: function (p) { return (p.optionDict || '') + '|' + (p.optionDictType || ''); },
    /** 取条件的字典选项（无缓存时返回空数组） */
    dictOptionsOf: function (p) { return this.dictOptCache[this.dictOptKey(p)] || []; },
    /** 拉取字典选项填入默认值下拉（同 key 只请求一次，先占位防并发重复） */
    loadDictOptions: function (p) {
      if (!this.isDictDefault(p)) return;
      var key = this.dictOptKey(p);
      if (this.dictOptCache[key]) return;
      this.dictOptCache[key] = [];
      var self = this;
      RS.post('/api/dictionaries/options', { code: p.optionDict, type: p.optionDictType || '' })
        .then(function (rows) { self.dictOptCache[key] = rows || []; })
        .catch(function (e) { delete self.dictOptCache[key]; RS.alertErr(e); }); // 失败清占位，下次展开可重试
    },
    /**
     * 取展示列对应的查询条件（按参数名 = 列名匹配，走 paramMap 索引），无则返回 undefined
     * @param {Object} col 展示列对象
     */
    colParam: function (col) {
      if (!col.colName) return undefined;
      // 英文列名走 paramName 复用；中文等列名在 columnName 字段上关联
      return (this.form.params || []).find(function (p) {
        return p.paramName === col.colName || p.columnName === col.colName;
      });
    },
    /**
     * 切换展示列的查询开关：开启即生成默认查询条件，关闭即移除
     * @param {Object} col 展示列对象
     * @param {boolean} on 开关目标状态
     */
    toggleColQuery: function (col, on) {
      if (!col.colName) {
        ElMessage.warning('请先填写该列的列名（对应 SQL 中的参数名）');
        return;
      }
      if (on) {
        if (this.colParam(col)) return;
        var p = this.emptyParam();
        p.label = col.label || col.colName;
        if (/^[A-Za-z_][A-Za-z0-9_$#]*$/.test(col.colName)) {
          p.paramName = col.colName;      // 英文合法标识符：参数名直接复用列名
        } else {
          // 中文等非标识符列名：列名存 columnName，参数名自动生成英文绑定名
          p.columnName = col.colName;
          var used = {};
          this.form.params.forEach(function (x) { if (x.paramName) used[x.paramName] = true; });
          var n = 1;
          while (used['cond' + n]) n++;
          p.paramName = 'cond' + n;
        }
        this.form.params.push(p);
      } else {
        this.form.params = this.form.params.filter(function (p) {
          return p.paramName !== col.colName && p.columnName !== col.colName;
        });
      }
    }
  }
}
</script>
