package engine

// Real gRPC adapter to the Java matching engine.
//
// Its only job is to translate between this package's plain Go types (which the
// HTTP/WS layers depend on) and the protobuf structs generated into
// genproto/. Keeping the translation here means the rest of the gateway never
// imports protobuf and stays decoupled from the wire format — dependency
// inversion: handlers depend on the EngineClient interface, not on gRPC.
//
// Stubs are generated from proto/openexchange.proto (see Makefile `proto` target).

import (
	"context"

	oepb "github.com/itsharsh007/openexchange/gateway/genproto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// GRPCClient is the production EngineClient backed by a gRPC connection.
type GRPCClient struct {
	conn   *grpc.ClientConn
	client oepb.MatchingEngineClient
}

// compile-time assertion that the real client satisfies the interface.
var _ EngineClient = (*GRPCClient)(nil)

// NewGRPCClient dials the engine. grpc.NewClient is lazy — it does not block on
// a live connection here; the first RPC establishes it (and each RPC carries a
// context deadline from the caller), so a briefly-unavailable engine doesn't
// stop the gateway from booting.
func NewGRPCClient(addr string) (*GRPCClient, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &GRPCClient{conn: conn, client: oepb.NewMatchingEngineClient(conn)}, nil
}

// Close releases the underlying connection.
func (g *GRPCClient) Close() error { return g.conn.Close() }

func (g *GRPCClient) SubmitOrder(ctx context.Context, o NewOrder) (OrderAck, error) {
	resp, err := g.client.SubmitOrder(ctx, &oepb.NewOrder{
		ClientOrderId: o.ClientOrderID,
		AccountId:     o.AccountID,
		Symbol:        o.Symbol,
		Side:          toProtoSide(o.Side),
		Type:          toProtoType(o.Type),
		PriceTicks:    o.PriceTicks,
		Quantity:      o.Quantity,
	})
	if err != nil {
		return OrderAck{}, err
	}
	return fromProtoAck(resp), nil
}

func (g *GRPCClient) CancelOrder(ctx context.Context, c CancelOrder) (OrderAck, error) {
	resp, err := g.client.CancelOrder(ctx, &oepb.CancelOrderRequest{
		OrderId:   c.OrderID,
		Symbol:    c.Symbol,
		AccountId: c.AccountID,
	})
	if err != nil {
		return OrderAck{}, err
	}
	return fromProtoAck(resp), nil
}

func (g *GRPCClient) GetBook(ctx context.Context, r BookRequest) (BookSnapshot, error) {
	resp, err := g.client.GetBook(ctx, &oepb.BookRequest{
		Symbol: r.Symbol,
		Depth:  r.Depth,
	})
	if err != nil {
		return BookSnapshot{}, err
	}
	return BookSnapshot{
		Symbol:   resp.GetSymbol(),
		Bids:     fromProtoLevels(resp.GetBids()),
		Asks:     fromProtoLevels(resp.GetAsks()),
		TsMillis: resp.GetTsMillis(),
	}, nil
}

// ── enum / message mapping (plain Go <-> protobuf) ───────────────────────────

func toProtoSide(s Side) oepb.Side {
	if s == SideSell {
		return oepb.Side_SELL
	}
	return oepb.Side_BUY
}

func toProtoType(t OrderType) oepb.OrderType {
	if t == OrderTypeMarket {
		return oepb.OrderType_MARKET
	}
	return oepb.OrderType_LIMIT
}

func fromProtoStatus(s oepb.OrderStatus) OrderStatus {
	switch s {
	case oepb.OrderStatus_ACCEPTED:
		return StatusAccepted
	case oepb.OrderStatus_PARTIALLY_FILLED:
		return StatusPartiallyFilled
	case oepb.OrderStatus_FILLED:
		return StatusFilled
	case oepb.OrderStatus_CANCELLED:
		return StatusCancelled
	default:
		return StatusRejected
	}
}

func fromProtoAck(a *oepb.OrderAck) OrderAck {
	return OrderAck{
		OrderID:        a.GetOrderId(),
		Status:         fromProtoStatus(a.GetStatus()),
		FilledQuantity: a.GetFilledQuantity(),
		Reason:         a.GetReason(),
	}
}

func fromProtoLevels(in []*oepb.PriceLevel) []PriceLevel {
	out := make([]PriceLevel, 0, len(in))
	for _, l := range in {
		out = append(out, PriceLevel{PriceTicks: l.GetPriceTicks(), Quantity: l.GetQuantity()})
	}
	return out
}
