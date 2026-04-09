#!/bin/bash

# ==== CONFIG ====
ACCOUNT_ID="570062"        # Номер аккаунта на my.selectel.ru (правый верхний угол)
USERNAME="Vert"  # Аккаунт → Пользователи → Сервисные пользователи
PASSWORD="Coci1488!Coci1488!Coci1488!Coci1488!"                   # Пароль сервисного пользователя
PROJECT_ID="a907e0f1b8a14d639ab5961884e49752"                  # Облачная платформа → Проекты → ID проекта

# ==== ПОЛУЧЕНИЕ ТОКЕНА (project-scoped) ====
TOKEN=$(curl -s -D /tmp/selectel_headers.txt -XPOST \
  -H 'Content-Type: application/json' \
  -d "{
    \"auth\": {
      \"identity\": {
        \"methods\": [\"password\"],
        \"password\": {
          \"user\": {
            \"name\": \"${USERNAME}\",
            \"domain\": {\"name\": \"${ACCOUNT_ID}\"},
            \"password\": \"${PASSWORD}\"
          }
        }
      },
      \"scope\": {
        \"project\": {\"id\": \"${PROJECT_ID}\"}
      }
    }
  }" \
  'https://cloud.api.selcloud.ru/identity/v3/auth/tokens' > /dev/null && \
  grep -i "^x-subject-token:" /tmp/selectel_headers.txt | awk '{print $2}' | tr -d '\r')

if [ -z "$TOKEN" ]; then
  echo "❌ Не удалось получить токен. Проверь ACCOUNT_ID, USERNAME, PASSWORD и PROJECT_ID."
  exit 1
fi

echo "✅ Токен получен:"
echo "$TOKEN"

# ==== ЗАПУСК СКРИПТА ====
# Раскомментируй строку ниже, чтобы сразу запустить script_selectel.mjs с токеном:
# node script_selectel.mjs "$TOKEN"