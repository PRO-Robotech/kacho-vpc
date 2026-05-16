package network

import (
	"context"
	"fmt"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
	"github.com/PRO-Robotech/kacho-vpc/internal/repo"
	kachorepo "github.com/PRO-Robotech/kacho-vpc/internal/repo/kacho"
)

// CreateDefaultSGUseCase — отдельный use-case для inline default-SG creation
// при Network.Create. Работает в УЖЕ открытой writer-TX (передаётся снаружи) —
// гарантирует atomic-семантику с Insert(Network). Сам TX не открывает и не
// commit'ит — это ответственность caller'а (`CreateNetworkUseCase.doCreate`).
//
// Skill evgeniy §7 I.9 (residual): «Inline default-SG creation в
// `NetworkService.Create` — fat service. Должна быть отдельная
// `CreateDefaultSGUseCase`, вызываемая из `CreateNetworkUseCase` через
// композицию». Atomic-семантика (skill I.10 — «orphan-resources = баг»)
// сохранена композицией use-case'ов в одной writer-TX: caller открывает Writer,
// вставляет Network, передаёт ОТКРЫТЫЙ writer сюда; здесь — Insert(SG) +
// outbox-emit + SetDefaultSGID(Network) + outbox-emit; caller делает Commit.
// Либо весь композит виден (Commit), либо ничего (Abort на любой ошибке).
//
// Stateless (без полей) — конструктор `NewCreateDefaultSGUseCase()` сохраняем
// для parity с остальными use-case'ами и удобства мокинга в будущем.
type CreateDefaultSGUseCase struct{}

// NewCreateDefaultSGUseCase создаёт stateless CreateDefaultSGUseCase.
func NewCreateDefaultSGUseCase() *CreateDefaultSGUseCase {
	return &CreateDefaultSGUseCase{}
}

// Execute создаёт default-SG для только-что-вставленной Network и проставляет
// её id как `Network.default_security_group_id`. Все DML и outbox-emit идут
// через переданный writer-TX (caller'а), что гарантирует atomic-семантику с
// Insert(Network) — либо все три DML видны, либо ни один (Abort/crash).
//
// Возвращает updated NetworkRecord с заполненным `default_security_group_id`.
// На любой ошибке возвращает уже обёрнутую через `mapRepoErr` gRPC-ошибку —
// caller просто пробрасывает её наверх (worker превратит в Operation.error).
func (u *CreateDefaultSGUseCase) Execute(
	ctx context.Context,
	w Writer,
	network domain.Network,
) (*kachorepo.NetworkRecord, error) {
	sg := domain.NewDefaultSecurityGroup(network)
	sgRec, err := w.SecurityGroups().Insert(ctx, &sg)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "SecurityGroup", sgRec.ID, "CREATED", securityGroupPayloadMap(sgRec)); err != nil {
		return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	upd, err := w.Networks().SetDefaultSGID(ctx, network.ID, sgRec.ID)
	if err != nil {
		return nil, mapRepoErr(err)
	}
	if err := w.Outbox().Emit(ctx, "Network", upd.ID, "UPDATED", networkPayloadMap(upd)); err != nil {
		return nil, mapRepoErr(fmt.Errorf("%w: outbox emit: %v", repo.ErrInternal, err))
	}
	return upd, nil
}
