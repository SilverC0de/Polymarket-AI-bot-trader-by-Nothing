package simulator

import (
	"testing"
)

func TestPotentialProfit(t *testing.T) {
	got := potentialProfit(20, 0.99)
	if got < 0.20 || got > 0.21 {
		t.Fatalf("potentialProfit(20, 0.99) = %.4f, want about 0.20", got)
	}

	got = potentialProfit(20, 0.85)
	if got < 3.52 || got > 3.54 {
		t.Fatalf("potentialProfit(20, 0.85) = %.4f, want about 3.53", got)
	}
}
