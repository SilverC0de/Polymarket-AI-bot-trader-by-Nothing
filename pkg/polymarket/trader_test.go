package polymarket

import "testing"

func TestMarketBuyAmountsCapsHighPriceAtNinetyNineCents(t *testing.T) {
	makerAmount, takerAmount, err := marketBuyAmounts(20, 0.9900000000000001)
	if err != nil {
		t.Fatalf("marketBuyAmounts returned error: %v", err)
	}

	if makerAmount != 19_999_980 {
		t.Fatalf("makerAmount = %d, want 19999980", makerAmount)
	}
	if takerAmount != 20_202_000 {
		t.Fatalf("takerAmount = %d, want 20202000", takerAmount)
	}

	price := float64(makerAmount) / float64(takerAmount)
	if price != 0.99 {
		t.Fatalf("price = %.12f, want 0.99", price)
	}
}

func TestMarketBuyAmountsRoundsWorstPriceToTick(t *testing.T) {
	makerAmount, takerAmount, err := marketBuyAmounts(20, 0.60)
	if err != nil {
		t.Fatalf("marketBuyAmounts returned error: %v", err)
	}

	price := float64(makerAmount) / float64(takerAmount)
	if price != 0.63 {
		t.Fatalf("price = %.12f, want 0.63", price)
	}
}
