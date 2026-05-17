#!/usr/bin/env bash
set -e

echo "==> Останавливаем snt-bot..."
ssh hostkey_us 'systemctl stop snt-bot'

echo "==> Чистим базу данных..."
ssh hostkey_us 'python3 -c "
import sqlite3
con = sqlite3.connect(\"/opt/snt-bot/snt.db\")
con.execute(\"DELETE FROM operations\")
con.execute(\"DELETE FROM sqlite_sequence WHERE name=\\\"operations\\\"\")
con.commit()
con.close()
print(\"OK\")
"'

echo "==> Запускаем snt-bot..."
ssh hostkey_us 'systemctl start snt-bot'

echo "==> Готово. Строк в базе: $(ssh hostkey_us 'python3 -c "import sqlite3; c=sqlite3.connect(\"/opt/snt-bot/snt.db\"); print(c.execute(\"SELECT COUNT(*) FROM operations\").fetchone()[0])"')"
