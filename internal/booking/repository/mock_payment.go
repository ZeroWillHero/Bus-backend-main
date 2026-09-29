package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/sl-bus/backend/internal/booking/domain"
)

// MockPaymentGateway implements domain.PaymentGateway for testing
// (ARCHITECTURE.md §3.3, Strategy pattern). Simulates approve/decline/timeout
// based on configurable triggers in the amount or details.
// Default behavior: always approves (real DirectPay SDK will swap this out later).
type MockPaymentGateway struct {
	DeclineIfAmountEndsWith01 bool   // If true, amounts ending in .01 are declined
	TimeoutIfReference        string // If set, requests with this reference timeout
}

func NewMockPaymentGateway() *MockPaymentGateway {
	return &MockPaymentGateway{
		DeclineIfAmountEndsWith01: true,
		TimeoutIfReference:        "TIMEOUT",
	}
}

func (g *MockPaymentGateway) Charge(ctx context.Context, amount float64, currency, reference string, details map[string]interface{}) (*domain.PaymentResult, error) {
	// Simulate timeout
	if g.TimeoutIfReference != "" && strings.Contains(reference, g.TimeoutIfReference) {
		return &domain.PaymentResult{
			Gateway:      "mock",
			Approved:     false,
			ErrorMessage: "simulated timeout",
		}, nil
	}

	// Simulate decline based on amount ending in .01
	if g.DeclineIfAmountEndsWith01 && amount*100 == float64(int(amount*100)) {
		fractionalCent := int(amount*100) % 100
		if fractionalCent == 1 {
			return &domain.PaymentResult{
				Gateway:      "mock",
				Approved:     false,
				ErrorMessage: "simulated decline (amount ends in .01)",
			}, nil
		}
	}

	// Default: always approve
	return &domain.PaymentResult{
		Gateway:       "mock",
		TransactionID: fmt.Sprintf("mock-txn-%s", reference),
		Approved:      true,
	}, nil
}

func (g *MockPaymentGateway) Refund(ctx context.Context, transactionID string, amount float64, currency string) error {
	// Mock always succeeds
	return nil
}
