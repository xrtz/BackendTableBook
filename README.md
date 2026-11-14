# TableBook backend

Go API для схемы зала и атомарного бронирования столов. План разработки и связь с Android-клиентом описаны в [ROADMAP.md](ROADMAP.md). HTTP-контракт хранится в [OpenAPI](api/openapi.yaml).

## Локальный запуск

Нужны Go 1.25+, `psql` и PostgreSQL с расширением `btree_gist`. Создай пустую базу `tablebook` и укажи её URL. Для подключения с паролем настрой файл паролей PostgreSQL или добавь пароль в URL в локальной переменной окружения.

```powershell
$env:TABLEBOOK_DATABASE_URL = 'postgres://postgres@127.0.0.1:5432/tablebook'
psql $env:TABLEBOOK_DATABASE_URL -v ON_ERROR_STOP=1 -f migrations/001_booking.sql
psql $env:TABLEBOOK_DATABASE_URL -v ON_ERROR_STOP=1 -f migrations/demo_seed.sql
$env:TABLEBOOK_DEMO_GUEST_TOKEN = [guid]::NewGuid().ToString('N')
$env:TABLEBOOK_DEMO_ADMIN_TOKEN = [guid]::NewGuid().ToString('N')
go run ./cmd/tablebook
```

Миграцию применяют один раз на пустой базе. Демо-сервер слушает `127.0.0.1:8080`. Токены из переменных окружения соответствуют двум локальным пользователям. Регистрация и полноценные сессии появятся на этапе 3.

Оставь сервер запущенным и открой второе окно PowerShell. Задай в нём `TABLEBOOK_DEMO_GUEST_TOKEN` с тем же значением, что и в первом окне.

```powershell
Invoke-RestMethod 'http://127.0.0.1:8080/v1/halls/1/floor-plan'
$headers = @{
    Authorization = "Bearer $env:TABLEBOOK_DEMO_GUEST_TOKEN"
    'Idempotency-Key' = [guid]::NewGuid().ToString('N')
}
$body = @{
    tableId = 1
    guestCount = 2
    startsAt = [DateTimeOffset]::UtcNow.AddHours(1).ToString("yyyy-MM-dd'T'HH:mm:ss'Z'")
} | ConvertTo-Json -Compress
Invoke-RestMethod 'http://127.0.0.1:8080/v1/reservations' -Method Post -ContentType 'application/json' -Headers $headers -Body $body
```

Повтор последней команды с теми же `$headers` и `$body` возвращает исходный ответ. С новым ключом запрос на тот же стол и время получает `409 TABLE_UNAVAILABLE`.

## Проверки

```powershell
go test ./...
go vet ./...
$env:TABLEBOOK_TEST_DATABASE_URL = $env:TABLEBOOK_DATABASE_URL
go test -count=5 -run TestPostgresBookingAPI -v ./internal/httpapi
```

Интеграционный тест требует права создать временную схему и установить `btree_gist` в `public`, если расширение ещё отсутствует. Используй локальную тестовую базу. Тест сам применяет миграцию и демо-данные в своей схеме и удаляет эту схему после прогона.
