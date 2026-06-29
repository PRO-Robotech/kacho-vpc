// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package network

import (
	"context"
	"fmt"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/shared/serviceerr"
	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo/helpers"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// CreateDefaultSGUseCase — отдельный use-case для inline default-SG creation
// при Network.Create. Работает в УЖЕ открытой writer-TX (передается снаружи) —
// гарантирует atomic-семантику с Insert(Network). Сам TX не открывает и не
// commit'ит — это ответственность caller'а (`CreateNetworkUseCase.doCreate`).
//
// Вынесен в отдельный use-case, чтобы не раздувать `NetworkService.Create`.
// Композиция в одной writer-TX исключает orphan-ресурсы: caller открывает
// Writer, вставляет Network, передает ОТКРЫТЫЙ writer сюда; здесь — Insert(SG) +
// outbox-emit + SetDefaultSGID(Network) + outbox-emit; caller делает Commit.
// Либо весь композит виден (Commit), либо ничего (Abort на любой ошибке).
//
// Stateless (без полей) — конструктор `NewCreateDefaultSGUseCase()` сохраняем
// для parity с остальными use-case'ами и удобства мокинга в будущем.
type CreateDefaultSGUseCase struct{}

// NewCreateDefaultSGUseCase создает stateless CreateDefaultSGUseCase.
func NewCreateDefaultSGUseCase() *CreateDefaultSGUseCase {
	return &CreateDefaultSGUseCase{}
}

// Execute создает default-SG для только-что-вставленной Network и проставляет
// ее id как `Network.default_security_group_id`. Все DML и outbox-emit идут
// через переданный writer-TX (caller'а), что гарантирует atomic-семантику с
// Insert(Network) — либо все три DML видны, либо ни один (Abort/crash).
//
// Возвращает updated NetworkRecord с заполненным `default_security_group_id`.
// На любой ошибке возвращает уже обернутую через `mapRepoErr` gRPC-ошибку —
// caller просто пробрасывает ее наверх (worker превратит в Operation.error).
func (u *CreateDefaultSGUseCase) Execute(
	ctx context.Context,
	w Writer,
	network domain.Network,
) (*kachorepo.NetworkRecord, error) {
	sg := domain.NewDefaultSecurityGroup(network)
	sgRec, err := w.SecurityGroups().Insert(ctx, &sg)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "SecurityGroup", sgRec.ID, "CREATED", helpers.DomainToMap(sgRec)); err != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	upd, err := w.Networks().SetDefaultSGID(ctx, network.ID, sgRec.ID)
	if err != nil {
		return nil, serviceerr.MapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "Network", upd.ID, "UPDATED", helpers.DomainToMap(upd)); err != nil {
		return nil, serviceerr.MapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	return upd, nil
}
