#!/bin/bash
# ==== CONFIG ====
ACCOUNTS_FILE="get_accounts.txt"

if [[ ! -f "$ACCOUNTS_FILE" ]]; then
    echo "❌ Файл $ACCOUNTS_FILE не найден."
    echo "📝 Создайте файл в формате: ACCOUNT_ID|USERNAME|PASSWORD|PROJECT_ID"
    exit 1
fi

echo "🚀 Начало обработки аккаунтов из $ACCOUNTS_FILE..."
echo "=========================================="

line_num=0
# Очищаем старый файл с токенами (если нужно сохранять историю - уберите эту строку)
> tokens.txt

while IFS='|' read -r ACCOUNT_ID USERNAME PASSWORD PROJECT_ID || [[ -n "$ACCOUNT_ID" ]]; do
    line_num=$((line_num + 1))

    # Пропускаем пустые строки и комментарии
    [[ -z "$ACCOUNT_ID" || "$ACCOUNT_ID" =~ ^[[:space:]]*# ]] && continue

    # Безопасное удаление только крайних пробелов и символов \r (Windows)
    ACCOUNT_ID=$(echo "$ACCOUNT_ID" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    USERNAME=$(echo "$USERNAME" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    PASSWORD=$(echo "$PASSWORD" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    PROJECT_ID=$(echo "$PROJECT_ID" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')

    echo ""
    echo "🔄 Аккаунт #$line_num | Project: $PROJECT_ID"

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
        echo "❌ Аккаунт #$line_num: Не удалось получить токен. Проверьте данные или лимиты API."
        continue
    fi

    echo "✅ Аккаунт #$line_num: Токен успешно получен!"
    echo "🔑 $TOKEN"

    # Сохраняем результат для дальнейшего использования
    echo "${TOKEN}|${PROJECT_ID}|" >> tokens.txt

done < "$ACCOUNTS_FILE"

echo ""
echo "🏁 Обработка завершена. Токены сохранены в tokens.txt"