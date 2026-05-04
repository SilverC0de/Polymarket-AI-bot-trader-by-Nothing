package polymarket

import (
	"encoding/json"
	"strings"
	"testing"
)

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

func TestPostOrderBodySerializesSaltAsNumber(t *testing.T) {
	body := postOrderBody{
		Order: orderWireBody{
			Salt: json.Number("78011896831834101158595025509514612777826333682964564215210608920542790493398"),
		},
		Owner:     "owner",
		OrderType: "FAK",
		DeferExec: false,
		PostOnly:  false,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	got := string(payload)
	if strings.Contains(got, `"salt":"`) {
		t.Fatalf("salt serialized as a string: %s", got)
	}
	if !strings.Contains(got, `"salt":78011896831834101158595025509514612777826333682964564215210608920542790493398`) {
		t.Fatalf("salt did not serialize as a JSON number: %s", got)
	}
	if !strings.Contains(got, `"deferExec":false`) || !strings.Contains(got, `"postOnly":false`) {
		t.Fatalf("expected explicit deferExec/postOnly flags: %s", got)
	}
}
