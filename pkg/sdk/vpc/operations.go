package vpc

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"

	operationv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/operation"
)

// OperationServiceClient — alias на сгенерированный gRPC-клиент LRO.
type OperationServiceClient = operationv1.OperationServiceClient

// GetOperation — sync read одной long-running operation по id.
func (c *Client) GetOperation(ctx context.Context, id string, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Operations.Get(ctx, &operationv1.GetOperationRequest{OperationId: id}, opts...)
}

// CancelOperation — sync cancel (если поддерживается доменом; VPC currently
// принимает запрос, но реально отмену делает не везде).
func (c *Client) CancelOperation(ctx context.Context, id string, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	return c.Operations.Cancel(ctx, &operationv1.CancelOperationRequest{OperationId: id}, opts...)
}

// defaultWaitPollInterval — дефолтная пауза между Operation.Get
// poll-итерациями. Подобрано под обычное время выполнения VPC-операций
// (доли секунды для CRUD; sub-second polling даёт быструю реакцию без
// бомбардировки api-gateway).
const defaultWaitPollInterval = 250 * time.Millisecond

// WaitForOperation pollит Operation.Get до тех пор, пока Operation.Done не
// станет true или контекст не отменится.
//
// `pollInterval` — пауза между poll-итерациями; если <= 0, используется
// defaultWaitPollInterval. Дедлайн / отмену задавать через ctx
// (context.WithTimeout / context.WithDeadline на стороне вызывающего).
//
// Возвращает финальный Operation: проверять `op.Result` (oneof error/response)
// для определения успеха/ошибки конкретной мутации. Сетевые ошибки между
// poll-ами оборачиваются и возвращаются без retry — за retry-семантику
// отвечает grpc-config (см. internal/clients/builder.go для production-
// builder'а на стороне сервиса; в SDK retry-config на усмотрение интегратора).
func (c *Client) WaitForOperation(ctx context.Context, operationID string, pollInterval time.Duration, opts ...grpc.CallOption) (*operationv1.Operation, error) {
	if operationID == "" {
		return nil, fmt.Errorf("vpcsdk: WaitForOperation: empty operationID")
	}
	if pollInterval <= 0 {
		pollInterval = defaultWaitPollInterval
	}

	for {
		op, err := c.GetOperation(ctx, operationID, opts...)
		if err != nil {
			return nil, fmt.Errorf("vpcsdk: WaitForOperation %s: get: %w", operationID, err)
		}
		if op.GetDone() {
			return op, nil
		}

		select {
		case <-ctx.Done():
			return op, ctx.Err()
		case <-time.After(pollInterval):
			// continue polling
		}
	}
}
