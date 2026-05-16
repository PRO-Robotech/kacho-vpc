// Package vpc предоставляет тонкую Go SDK-обёртку над gRPC-stubs Kachō VPC
// API для внешних интеграторов.
//
// # Назначение
//
// Сервисное репо kacho-vpc — внутренний control-plane, а сгенерированные
// proto-stubs живут в централизованном репо kacho-proto
// (github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1). Чтобы
// интегратору не пришлось руками открывать gRPC-соединение и регистрировать
// семь публичных service-клиентов плюс OperationService, этот пакет
// предоставляет единый Client с типизированными accessors по каждому ресурсу.
//
// SDK — НЕ бизнес-логика и НЕ серверная составляющая: это compile-time-thin
// wrapper, добавляющий единую точку Dial/Close и удобную работу с
// long-running Operations.
//
// Минимальный пример
//
//	import (
//	    vpcsdk "github.com/PRO-Robotech/kacho-vpc/pkg/sdk/vpc"
//	    vpcv1  "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
//	    "google.golang.org/grpc"
//	    "google.golang.org/grpc/credentials"
//	)
//
//	creds := credentials.NewTLS(nil)
//	c, err := vpcsdk.NewClient("vpc.kacho.local:443",
//	    grpc.WithTransportCredentials(creds))
//	if err != nil { return err }
//	defer c.Close()
//
//	resp, err := c.Networks.List(ctx, &vpcv1.ListNetworksRequest{FolderId: "..."})
//
// # Long-running Operations
//
// Все мутации публичных VPC RPC возвращают operation.Operation (см.
// workspace-CLAUDE.md «API contract — flat resources + Operations»).
// Convenience-метод WaitForOperation pollит OperationService.Get до тех пор,
// пока operation.done не станет true или контекст не отменится:
//
//	op, _ := c.Networks.Create(ctx, createReq)        // op.Done==false
//	done, err := c.WaitForOperation(ctx, op.Id, 500*time.Millisecond)
//	// done.Done == true, done.Result содержит response/error.
//
// Skill evgeniy §1 A.2: внешний SDK живёт в pkg/sdk/<service>/ как тонкая
// обёртка; бизнес-логику в SDK не помещать.
package vpc
