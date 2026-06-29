# Copyright (c) PRO-Robotech
# SPDX-License-Identifier: BUSL-1.1

"""Observability case-set для kacho-vpc (класс `OBS`).

Покрывает:
- `OBS-REQID-HEADER-ECHO` — verify X-Request-Id propagation через api-gateway

За границей newman (legitimate boundary):
- `OBS-METRICS-*` cases — требуют :9090 reachable из newman runner. Текущий vpc
  k8s Service не exposes :9090 (только :8080 для gRPC). Это **legitimate boundary**,
  не tech-debt: metrics scrape live делает Prometheus operator внутри cluster,
  не newman. Для VPC newman-based metrics verification нужен expose `:9090`
  в kacho-deploy/Helm chart Service spec.
"""

CASES = []


CASES.append(Case(
    id="OBS-REQID-HEADER-ECHO",
    title="X-Request-Id header propagation: client sends → response echoes same id (observability trace)",
    classes=["OBS"], priority="P2",
    steps=[
        Step(
            name="list-with-reqid",
            method="GET",
            path="/vpc/v1/networks?projectId={{_suiteProjectId}}&pageSize=1",
            pre_script=[
                # Step.headers field не существует — set через pre_script (см. gen.py Step dataclass).
                "pm.request.headers.upsert({key: 'X-Request-Id', value: 'obs-reqid-echo-' + pm.environment.get('runId')});",
            ],
            test_script=[
                "pm.test('list 200', () => pm.expect(pm.response.code).to.eql(200));",
                "pm.test('X-Request-Id echoed in response headers', () => {",
                "  const echoed = pm.response.headers.get('X-Request-Id') || pm.response.headers.get('x-request-id');",
                "  const expected = 'obs-reqid-echo-' + pm.environment.get('runId');",
                "  pm.expect(echoed, `expected=${expected} headers=${JSON.stringify(pm.response.headers.toObject())}`).to.eql(expected);",
                "});",
            ],
        ),
    ],
))
