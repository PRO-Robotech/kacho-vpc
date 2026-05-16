// Package vpc — публичный SDK для Kachō VPC API (KAC-94, skill evgeniy §1 A.2).
// См. doc.go для рассказа о назначении пакета.
package vpc

import (
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	operationv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
	privatelinkv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1/privatelink"
)

// Client — тонкая обёртка над gRPC-соединением к Kachō VPC API.
//
// Один Client = одно gRPC-соединение (resolved через grpc.NewClient — lazy
// dial). Все 8 публичных service-клиентов VPC и OperationService разделяют
// этот conn — Postgres-аналогии «один pool на всё»: ровно так же gRPC любит
// единое долгоживущее соединение, переиспользуемое HTTP/2-стримами.
//
// Внутренние (admin) сервисы — InternalNetworkService, InternalAddressService,
// InternalAddressPoolService и т.п. — в SDK сознательно НЕ экспонируются:
// они доступны только на cluster-internal 9091 порту (см. workspace-CLAUDE.md
// «Запреты» #6 + «Инфра-чувствительные данные») и не предназначены для
// external integrators.
type Client struct {
	conn *grpc.ClientConn

	// Public VPC services (7 verbatim YC-style ресурсов + NetworkInterface).
	Networks          vpcv1.NetworkServiceClient
	Subnets           vpcv1.SubnetServiceClient
	Addresses         vpcv1.AddressServiceClient
	RouteTables       vpcv1.RouteTableServiceClient
	SecurityGroups    vpcv1.SecurityGroupServiceClient
	Gateways          vpcv1.GatewayServiceClient
	PrivateEndpoints  privatelinkv1.PrivateEndpointServiceClient
	NetworkInterfaces vpcv1.NetworkInterfaceServiceClient

	// Operations — для poll-after-mutation; все мутации в VPC возвращают
	// operation.Operation (LRO). См. WaitForOperation().
	Operations operationv1.OperationServiceClient
}

// NewClient создаёт VPC SDK client поверх gRPC-соединения по адресу addr.
//
// Если ни одной grpc.DialOption не передано — соединение insecure (для
// dev / port-forward). В production интеграторы обязаны передать
// grpc.WithTransportCredentials(credentials.NewTLS(...)) или иной
// TLS-credentials provider.
//
// grpc.NewClient (replacement для устаревшего grpc.Dial) — lazy: dial
// происходит при первом RPC, не при NewClient. Поэтому ошибка тут означает
// проблему конфигурации (некорректный target syntax), а не недоступность
// backend'а.
func NewClient(addr string, opts ...grpc.DialOption) (*Client, error) {
	if strings.TrimSpace(addr) == "" {
		return nil, fmt.Errorf("vpcsdk: empty addr")
	}
	if len(opts) == 0 {
		opts = []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	}
	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, fmt.Errorf("vpcsdk: grpc.NewClient %q: %w", addr, err)
	}
	return newClientFromConn(conn), nil
}

// NewClientFromConn оборачивает уже открытое gRPC-соединение в SDK Client.
// Полезно когда интегратор уже владеет соединением (общий pool, mTLS-handshake,
// service-mesh sidecar) и хочет переиспользовать его для VPC API без второго
// dial. Close() в этом случае закрывает переданный conn — владение
// передаётся SDK.
func NewClientFromConn(conn *grpc.ClientConn) *Client {
	return newClientFromConn(conn)
}

func newClientFromConn(conn *grpc.ClientConn) *Client {
	return &Client{
		conn:              conn,
		Networks:          vpcv1.NewNetworkServiceClient(conn),
		Subnets:           vpcv1.NewSubnetServiceClient(conn),
		Addresses:         vpcv1.NewAddressServiceClient(conn),
		RouteTables:       vpcv1.NewRouteTableServiceClient(conn),
		SecurityGroups:    vpcv1.NewSecurityGroupServiceClient(conn),
		Gateways:          vpcv1.NewGatewayServiceClient(conn),
		PrivateEndpoints:  privatelinkv1.NewPrivateEndpointServiceClient(conn),
		NetworkInterfaces: vpcv1.NewNetworkInterfaceServiceClient(conn),
		Operations:        operationv1.NewOperationServiceClient(conn),
	}
}

// Conn возвращает underlying gRPC-соединение (read-only access). Может
// пригодиться, если интегратор хочет зарегистрировать ещё один stub-клиент
// поверх того же соединения (например, custom Internal* в admin-tooling
// внутри кластера).
func (c *Client) Conn() *grpc.ClientConn { return c.conn }

// Close закрывает gRPC-соединение. Идемпотентен на уровне grpc.ClientConn.
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
