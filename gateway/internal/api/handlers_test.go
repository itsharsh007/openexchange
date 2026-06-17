package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/itsharsh007/openexchange/gateway/internal/config"
	"github.com/itsharsh007/openexchange/gateway/internal/engine"
	"github.com/itsharsh007/openexchange/gateway/internal/ws"
)

// fakeGate blocks exactly one account, so we can drive both branches of the gate.
type fakeGate struct{ blocked string }

func (f fakeGate) Blocked(a string) (bool, string) {
	if a == f.blocked {
		return true, "over cap"
	}
	return false, ""
}

// capturePub records what the order feed was asked to publish.
type capturePub struct{ submits []engine.OrderAck }

func (p *capturePub) PublishSubmit(_ engine.NewOrder, ack engine.OrderAck) { p.submits = append(p.submits, ack) }
func (p *capturePub) PublishCancel(engine.CancelOrder, engine.OrderAck)     {}

func newTestServer(gate RiskGate, pub *capturePub) *Server {
	return NewServer(
		&config.Config{EngineTimeout: time.Second},
		engine.NewMockClient(),
		nil, // cache: unused on the submit path
		ws.NewHub(),
		pub,
		gate,
	)
}

func submit(t *testing.T, srv *Server, account string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"account_id":"` + account + `","symbol":"AAPL","side":"BUY","type":"LIMIT","price_ticks":10000,"quantity":5}`
	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleSubmit(w, req)
	return w
}

func TestSubmitBlockedByRiskGate(t *testing.T) {
	pub := &capturePub{}
	srv := newTestServer(fakeGate{blocked: "bad-acct"}, pub)

	w := submit(t, srv, "bad-acct")

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, "risk: over cap") {
		t.Errorf("response should carry the risk reason, got %q", body)
	}
	// The rejected attempt must still be published to the orders feed, as REJECTED.
	if len(pub.submits) != 1 || pub.submits[0].Status != engine.StatusRejected {
		t.Errorf("expected one published REJECTED attempt, got %+v", pub.submits)
	}
}

func TestSubmitAllowedWhenNotGated(t *testing.T) {
	pub := &capturePub{}
	srv := newTestServer(fakeGate{blocked: "bad-acct"}, pub)

	w := submit(t, srv, "good-acct")

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", w.Code)
	}
	if len(pub.submits) != 1 || pub.submits[0].Status != engine.StatusAccepted {
		t.Errorf("expected one published ACCEPTED order, got %+v", pub.submits)
	}
}

// AllowAllGate is the production default when no risk feed is wired; it must pass.
func TestAllowAllGate(t *testing.T) {
	pub := &capturePub{}
	srv := newTestServer(AllowAllGate{}, pub)
	if w := submit(t, srv, "anyone"); w.Code != http.StatusCreated {
		t.Fatalf("AllowAllGate should never block; status=%d", w.Code)
	}
}
