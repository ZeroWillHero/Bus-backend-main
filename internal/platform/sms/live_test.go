//go:build smslive

package sms

// Live checks against the real SMSlenz API, excluded from normal runs.
// Reads credentials from sl-bus-backend/.env (or the environment):
//
//	go test -tags smslive -run TestLiveAccountStatus ./internal/platform/sms -v
//	SMS_LIVE_TO=07XXXXXXXX go test -tags smslive -run TestLiveSend ./internal/platform/sms -v   # costs 1 SMS

import (
	"context"
	"os"
	"testing"

	"github.com/joho/godotenv"
)

func liveClient(t *testing.T) *SMSlenzClient {
	t.Helper()
	_ = godotenv.Load("../../../.env")
	_, c, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if c == nil {
		t.Skip("SMSLENZ_USER_ID / SMSLENZ_API_KEY not set")
	}
	return c
}

func TestLiveAccountStatus(t *testing.T) {
	st, err := liveClient(t).AccountStatus(context.Background())
	if err != nil {
		t.Fatalf("AccountStatus: %v", err)
	}
	t.Logf("status=%s main_balance=%.2f sender_balances=%v", st.Status, st.CreditBalance, st.SenderBalances)
}

func TestLiveSend(t *testing.T) {
	to, err := NormalizeLKMobile(os.Getenv("SMS_LIVE_TO"))
	if err != nil {
		t.Skip("set SMS_LIVE_TO to a Sri Lankan mobile number")
	}
	rec, err := liveClient(t).Send(context.Background(), to, "SL-Bus test message. No action needed.")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	t.Logf("sent to %s: campaign_id=%s segments=%d", Mask(to), rec.ProviderRef, rec.Segments)
}
