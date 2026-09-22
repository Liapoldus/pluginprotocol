# Golden vectors `liapoldus.plugin.v1`

Файл `golden.json` содержит канонические wire frames вместе с 4-байтовым
length-prefix. Векторы являются частью wire-контракта: изменение байтов,
номеров полей или enum-значений требует новой major-версии протокола.

Проверка выполняется TypeScript-набором из `tests/` через `protocol-probe`.
Транспортный префикс кодируется в big-endian и проверяется вместе с payload.
