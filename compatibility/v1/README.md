# Совместимость plugin protocol v1

Переход с `v1.0.0` length-prefixed frames на gRPC/HTTP/2 — намеренный breaking
change внутри module major v1 по явному решению проекта. Следующий запланированный
tag — `v1.1.0`; старые плагины необходимо обновить одновременно с Gateway.
Старый framing больше не поддерживается и не имеет wire-hex golden vectors.

Совместимость текущего контракта проверяется generated protobuf/gRPC stubs,
protobuf service conformance и JSON payload vectors в
`contracts/protocol/v1/json-payload-vectors.json`. Declarative contracts
capability хранятся рядом с соответствующими plugin contracts.
