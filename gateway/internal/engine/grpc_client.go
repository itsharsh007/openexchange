package engine

// ─────────────────────────────────────────────────────────────────────────────
// HOW TO SWAP IN THE REAL gRPC CLIENT
//
// This file is intentionally NOT compiled-active gRPC code, because protoc
// stubs are not generated yet and we must keep `go build ./...` working with
// zero protoc dependency. When the stubs exist, do the following:
//
// 1. Generate Go stubs from proto/openexchange.proto. From the repo root:
//
//      protoc \
//        --go_out=. --go_opt=module=github.com/itsharsh007/openexchange/gateway \
//        --go-grpc_out=. --go-grpc_opt=module=github.com/itsharsh007/openexchange/gateway \
//        proto/openexchange.proto
//
//    (NOTE: proto/openexchange.proto currently declares
//       option go_package = "github.com/harshsharma/openexchange/gateway/genproto;oepb";
//    but this module is github.com/itsharsh007/...  — reconcile the module path
//    in the proto's go_package before generating, or the import below will not
//    resolve. Contracts-first: change the proto, regenerate, then wire here.)
//
// 2. Add the runtime deps:
//      go get google.golang.org/grpc google.golang.org/protobuf
//
// 3. Replace the body below with the real adapter. The adapter's only job is to
//    translate between our plain Go types (this package) and the generated
//    protobuf structs, so the rest of the gateway stays decoupled from gRPC.
//
// Example real implementation (uncomment, fix the import path, and delete the
// MockClient from main once ready):
//
//   import (
//       "context"
//       "google.golang.org/grpc"
//       "google.golang.org/grpc/credentials/insecure"
//       oepb "github.com/itsharsh007/openexchange/gateway/genproto"
//   )
//
//   type GRPCClient struct {
//       conn   *grpc.ClientConn
//       client oepb.MatchingEngineClient
//   }
//
//   func NewGRPCClient(addr string) (*GRPCClient, error) {
//       conn, err := grpc.NewClient(addr,
//           grpc.WithTransportCredentials(insecure.NewCredentials()))
//       if err != nil {
//           return nil, err
//       }
//       return &GRPCClient{conn: conn, client: oepb.NewMatchingEngineClient(conn)}, nil
//   }
//
//   func (g *GRPCClient) Close() error { return g.conn.Close() }
//
//   var _ EngineClient = (*GRPCClient)(nil)
//
//   func (g *GRPCClient) SubmitOrder(ctx context.Context, o NewOrder) (OrderAck, error) {
//       resp, err := g.client.SubmitOrder(ctx, &oepb.NewOrder{
//           ClientOrderId: o.ClientOrderID,
//           AccountId:     o.AccountID,
//           Symbol:        o.Symbol,
//           Side:          toProtoSide(o.Side),
//           Type:          toProtoType(o.Type),
//           PriceTicks:    o.PriceTicks,
//           Quantity:      o.Quantity,
//       })
//       if err != nil {
//           return OrderAck{}, err
//       }
//       return OrderAck{
//           OrderID:        resp.OrderId,
//           Status:         fromProtoStatus(resp.Status),
//           FilledQuantity: resp.FilledQuantity,
//           Reason:         resp.Reason,
//       }, nil
//   }
//
//   // CancelOrder and GetBook follow the same translate-call-translate shape;
//   // toProtoSide/fromProtoStatus etc. are tiny enum mapping helpers.
//
// Then in cmd/gateway/main.go, replace:
//      eng := engine.NewMockClient()
// with:
//      eng, err := engine.NewGRPCClient(cfg.EngineGRPCAddr)
//      if err != nil { log.Fatal(err) }
//      defer eng.Close()
// ─────────────────────────────────────────────────────────────────────────────
