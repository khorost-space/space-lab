package main

import (
	"testing"
	"time"
)

// TestRotateEveryIsHalfTTL: перезапись строго раньше истечения — иначе
// существует окно, в котором в файле лежит просроченный токен.
func TestRotateEveryIsHalfTTL(t *testing.T) {
	if got := rotateEvery(600 * time.Second); got != 300*time.Second {
		t.Errorf("период = %v, ожидалось 300s", got)
	}
	if rotateEvery(600*time.Second) >= 600*time.Second {
		t.Error("период ротации не меньше TTL")
	}
}
