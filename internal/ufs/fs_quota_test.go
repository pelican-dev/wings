package ufs

import (
	"math"
	"testing"
)

func TestQuotaCanFitRejectsOverflowingSize(t *testing.T) {
	q := NewQuota(nil, 1<<30)
	q.SetUsage(1)

	if q.CanFit(math.MaxInt64) {
		t.Fatal("expected oversized write to be rejected")
	}

	q.SetUsage(1 << 20)
	if q.CanFit(math.MaxInt64 - 100) {
		t.Fatal("expected oversized write to be rejected")
	}
}

func TestQuotaCanFitAllowsShrinkingWrite(t *testing.T) {
	q := NewQuota(nil, 10)
	q.SetUsage(20)

	if !q.CanFit(-5) {
		t.Fatal("expected shrinking write to be allowed")
	}
}