# История изменений

## v1.0.0 — первый стабильный wire-контракт

- Зафиксирован пакет `liapoldus.plugin.v1`.
- Источники protobuf разделены на `frame.proto`, `envelope.proto` и
  `control.proto` без изменения package, import path и номеров полей.
- Зафиксированы framing, session и control boundaries.
- Добавлены golden vectors для совместимости реализаций.
