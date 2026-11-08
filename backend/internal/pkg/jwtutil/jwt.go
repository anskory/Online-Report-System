// Package jwtutil 提供 JWT 签发与解析（access/refresh 双令牌）。
package jwtutil

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims 自定义声明
type Claims struct {
	UserID   int64  `json:"uid"`
	Username string `json:"name"`
	Role     string `json:"role"`
	Typ      string `json:"typ"` // access | refresh
	jwt.RegisteredClaims
}

const (
	accessTTL  = 2 * time.Hour
	refreshTTL = 7 * 24 * time.Hour
)

// Sign 签发令牌，typ 为 "access" 或 "refresh"
func Sign(secret, typ string, userID int64, username, role string) (string, error) {
	ttl := accessTTL
	if typ == "refresh" {
		ttl = refreshTTL
	}
	claims := &Claims{
		UserID:   userID,
		Username: username,
		Role:     role,
		Typ:      typ,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

// Parse 解析并校验令牌
func Parse(secret, tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("非法签名算法")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("令牌无效")
	}
	return claims, nil
}
