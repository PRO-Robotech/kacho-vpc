package domain

import "github.com/PRO-Robotech/kacho-corelib/ids"

// Wave 2 pilot — builders для inline-собираемых domain-сущностей вокруг Network.
// Skill evgeniy §4 D.7 / AP-2: запрет inline-литералов с magic-константами в
// service-слое. Эти функции живут в domain-пакете и инкапсулируют:
//   * имя default-SG (formula: "default-sg-" + TruncateID(networkID)),
//   * описание default-SG,
//   * status (`ACTIVE`) — через SecurityGroupStatus enum, а не голую строку,
//   * default-правила (INGRESS+EGRESS, protocol ANY, 0.0.0.0/0).

// DefaultSGName возвращает имя default-SG для сети. Формула — verbatim
// kacho-vpc до Wave 2 (см. service/network.go::doCreate до KAC-99):
// `default-sg-<first 8 chars of network id>`.
func DefaultSGName(networkID string) string {
	return "default-sg-" + TruncateID(networkID)
}

// DefaultSGDescription — описание default-SG. Verbatim kacho-vpc.
const DefaultSGDescription = "Default security group (auto-created by kacho-vpc)"

// NewDefaultSecurityGroupRules возвращает дефолтный набор правил, который
// получает каждый автосозданный default-SG: разрешить весь INGRESS и EGRESS
// от/в 0.0.0.0/0 (verbatim YC: 2 правила, protocol=ANY (=-1), v4 cidr `0.0.0.0/0`).
//
// Это builder, а не глобальная переменная — каждый вызов отдаёт fresh slice
// (caller может мутировать без побочных эффектов).
func NewDefaultSecurityGroupRules() []SecurityGroupRule {
	return []SecurityGroupRule{
		{Direction: "INGRESS", ProtocolName: "ANY", ProtocolNumber: -1, V4CidrBlocks: []string{"0.0.0.0/0"}},
		{Direction: "EGRESS", ProtocolName: "ANY", ProtocolNumber: -1, V4CidrBlocks: []string{"0.0.0.0/0"}},
	}
}

// NewDefaultSecurityGroup собирает domain.SecurityGroup для default-SG сети.
// CreatedAt сюда не входит (DB-managed); caller (репозиторий) выставит время
// в Insert. Status — через enum-константу, а не string-literal.
//
// Используется service-слоем в worker'е Network.Create при
// KACHO_VPC_DEFAULT_SG_INLINE=true (skill evgeniy §4 D.7).
func NewDefaultSecurityGroup(net Network) SecurityGroup {
	return SecurityGroup{
		ID:                ids.NewID(ids.PrefixSecurityGroup),
		FolderID:          net.FolderID,
		NetworkID:         net.ID,
		Name:              DefaultSGName(net.ID),
		Description:       DefaultSGDescription,
		Status:            string(SecurityGroupStatusActive),
		DefaultForNetwork: true,
		Rules:             NewDefaultSecurityGroupRules(),
	}
}
