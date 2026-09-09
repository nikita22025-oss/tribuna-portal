# Backend

Три бинарника из build-гайда собираются командами:

```sh
go build -o trbn-backend ./cmd/server
go build -o trbn-ingester ./cmd/ingester
go build -o trbn-admin ./cmd/admin
```

На первом этапе используются fixture-данные. Источники новостей подключаются только после подтверждения разрешённого RSS/API-доступа.
