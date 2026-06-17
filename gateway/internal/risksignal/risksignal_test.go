package risksignal

import (
	"testing"

	"google.golang.org/protobuf/proto"

	oepb "github.com/itsharsh007/openexchange/gateway/genproto"
)

func mustMarshal(t *testing.T, s *oepb.RiskSignal) []byte {
	t.Helper()
	b, err := proto.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestDecodeSignal(t *testing.T) {
	raw := mustMarshal(t, &oepb.RiskSignal{
		Kind:      oepb.SignalKind_RISK_EXPOSURE,
		AccountId: "a1",
		Score:     1.0,
		Action:    oepb.SignalAction_REJECT,
		Reason:    "over cap",
	})
	got, err := decodeSignal(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.GetAccountId() != "a1" || got.GetAction() != oepb.SignalAction_REJECT || got.GetReason() != "over cap" {
		t.Errorf("decoded wrong: %+v", got)
	}
}

func TestGateBlockThenClear(t *testing.T) {
	g := NewGate()

	// Unknown account: fail-open.
	if blocked, _ := g.Blocked("a1"); blocked {
		t.Fatal("unknown account should not be blocked")
	}

	// A REJECT blocks the account with its reason.
	g.apply(&oepb.RiskSignal{AccountId: "a1", Action: oepb.SignalAction_REJECT, Reason: "over cap", Score: 1})
	blocked, reason := g.Blocked("a1")
	if !blocked || reason != "over cap" {
		t.Fatalf("expected blocked with reason, got blocked=%v reason=%q", blocked, reason)
	}

	// A later ALLOW clears it.
	g.apply(&oepb.RiskSignal{AccountId: "a1", Action: oepb.SignalAction_ALLOW, Reason: "within limits"})
	if blocked, _ := g.Blocked("a1"); blocked {
		t.Fatal("ALLOW should clear the block")
	}
}

// An empty account id is ignored (defensive: never block "all unattributed orders").
func TestGateIgnoresEmptyAccount(t *testing.T) {
	g := NewGate()
	g.apply(&oepb.RiskSignal{AccountId: "", Action: oepb.SignalAction_REJECT, Reason: "x"})
	if blocked, _ := g.Blocked(""); blocked {
		t.Fatal("empty account must never be blocked")
	}
}
