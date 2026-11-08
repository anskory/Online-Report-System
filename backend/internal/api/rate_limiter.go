package api

import (
	"sync"
	"time"
)

// RateLimiter 简单滑动窗口限流（按 key，通常为客户端 IP）
type RateLimiter struct {
	mu     sync.Mutex
	visits map[string][]time.Time
	limit  int               // 窗口内最大次数
	window time.Duration     // 窗口时长
}

func NewRateLimiter(limitPerMin int) *RateLimiter {
	return &RateLimiter{
		visits: map[string][]time.Time{},
		limit:  limitPerMin,
		window: time.Minute,
	}
}

// maxKeys key 数量上限：公开接口按客户端 IP 限流，恶意请求可用海量不同 IP
// 撑大 map 造成内存泄漏，达到上限时触发一次全量清扫（惰性过期，无后台协程）
const maxKeys = 10000

// Allow 判断 key 在当前窗口内是否允许访问
func (r *RateLimiter) Allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-r.window)
	// key 数超限时全量清扫过期 key，防止 map 无限增长
	if len(r.visits) > maxKeys {
		for k, ts := range r.visits {
			fresh := false
			for _, t := range ts {
				if t.After(cutoff) {
					fresh = true
					break
				}
			}
			if !fresh {
				delete(r.visits, k)
			}
		}
	}
	// 清理过期记录
	kept := r.visits[key][:0]
	for _, t := range r.visits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= r.limit {
		r.visits[key] = kept
		return false
	}
	r.visits[key] = append(kept, now)
	return true
}
