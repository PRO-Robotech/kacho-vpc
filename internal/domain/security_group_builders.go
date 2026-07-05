// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package domain

// Builders для inline-собираемых domain-сущностей вокруг Network. Держат подальше
// от service-слоя inline-литералы с magic-константами, инкапсулируя:
//   * имя default-SG (formula: "default-sg-" + TruncateID(networkID)),
//   * описание default-SG,
//   * default-правила (INGRESS+EGRESS, protocol ANY, 0.0.0.0/0).

// DefaultSGName возвращает имя default-SG для сети по формуле
// `default-sg-<first 8 chars of network id>`.
func DefaultSGName(networkID string) string {
	return "default-sg-" + TruncateID(networkID)
}

// DefaultSGDescription — описание автосоздаваемого default-SG.
const DefaultSGDescription = "Default security group (auto-created by kacho-vpc)"

// NewDefaultSecurityGroupRules возвращает дефолтный набор правил, который
// получает каждый автосозданный default-SG: разрешить весь INGRESS и EGRESS
// от/в 0.0.0.0/0 (2 правила, protocol=ANY (=-1), v4 cidr `0.0.0.0/0`).
//
// Это builder, а не глобальная переменная — каждый вызов отдает fresh slice
// (caller может мутировать без побочных эффектов). Direction — enum
// `SecurityGroupRuleDirection`, а не голая string-literal.
func NewDefaultSecurityGroupRules() []SecurityGroupRule {
	return []SecurityGroupRule{
		{Direction: SecurityGroupRuleDirectionIngress, ProtocolName: "ANY", ProtocolNumber: -1, V4CidrBlocks: []string{"0.0.0.0/0"}},
		{Direction: SecurityGroupRuleDirectionEgress, ProtocolName: "ANY", ProtocolNumber: -1, V4CidrBlocks: []string{"0.0.0.0/0"}},
	}
}

// NewDefaultSecurityGroup собирает domain.SecurityGroup для default-SG сети.
// Чистый value-builder: `id` минтит use-case/repo-слой (ids.NewID(PrefixSecurityGroup))
// и передаёт сюда — domain не тянет infra-утилиту и остаётся детерминированным
// (stdlib+proto-only, dependency-rule). CreatedAt сюда не входит (DB-managed);
// caller (репозиторий) выставит время в Insert. Name/Description — newtypes
// (RcNameVPC / RcDescription).
//
// Используется service-слоем в worker'е Network.Create при
// KACHO_VPC_DEFAULT_SG_INLINE=true.
func NewDefaultSecurityGroup(id string, net Network) SecurityGroup {
	return SecurityGroup{
		ID:                id,
		ProjectID:         net.ProjectID,
		NetworkID:         net.ID,
		Name:              RcNameVPC(DefaultSGName(net.ID)),
		Description:       RcDescription(DefaultSGDescription),
		DefaultForNetwork: true,
		Rules:             NewDefaultSecurityGroupRules(),
	}
}
