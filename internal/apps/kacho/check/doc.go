// Package check содержит kacho-vpc per-service Check-interceptor wiring под
// E3 / KAC-108 (см. acceptance §6 D4).
//
// Состав:
//   - permission_map.go   — RPCMap для всех публичных RPC kacho-vpc
//     (9 сервисов × ≈ 5-10 методов: Network, Subnet, Address, RouteTable,
//     SecurityGroup, Gateway, PrivateEndpoint, NetworkInterface + Operation).
//   - check_client.go     — gRPC adapter поверх `iamv1.InternalIAMServiceClient.Check`
//     (реализует port `authz.CheckClient` из kacho-corelib/authz).
//   - factory.go          — фабрика, собирающая `*authz.Interceptor` из config
//     (IAM-conn + breakglass + cache + map). Возвращает nil-safe interceptor
//     если IAM-conn не сконфигурирован (graceful start без kacho-iam в dev).
//
// Wiring (composition root — `cmd/vpc/main.go`):
//
//	authzIntr, err := check.NewInterceptor(check.Options{
//	    ServiceName: "kacho-vpc",
//	    IAMConn:     iamConn,         // *grpc.ClientConn к kacho-iam:9091
//	    Breakglass:  cfg.AuthZ.Breakglass,
//	    Logger:      logger,
//	})
//	if err != nil { return err }
//	if authzIntr != nil {
//	    grpcSrv := grpcsrv.NewServer(
//	        grpc.ChainUnaryInterceptor(
//	            handler.TenantUnaryInterceptor(false, productionMode),
//	            authzIntr.Unary(),
//	        ),
//	        grpc.ChainStreamInterceptor(
//	            handler.TenantStreamInterceptor(false, productionMode),
//	            authzIntr.Stream(),
//	        ),
//	    )
//	}
//
// Cache-invalidation (LISTEN/NOTIFY → `kacho_iam_subjects`) — НЕ wired в этом
// MVP (см. scope-guard в KAC-108): достаточно TTL=5s + outbox-drain≤2s
// → ≤10s revoke propagation. Listen-loop добавится в follow-up при наличии
// DSN'а на kacho-iam Postgres.
package check
