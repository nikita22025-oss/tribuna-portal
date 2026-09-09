# Запуск на Ubuntu 24.04

Архив рассчитан на обычный VPS Linux amd64. ARM-серверу нужна сборка GOARCH=arm64. Для готовых бинарников Go на сервере не требуется.

Ниже команды для администратора сервера; они **не выполнялись** на сервере заказчика. Перед обновлением существующего сайта сделайте резервную копию, а для нового развертывания используйте отдельный каталог.

## Установка

```bash
sudo apt update
sudo apt install -y nginx certbot python3-certbot-nginx sqlite3
sudo useradd --system --home /opt/tribuna --shell /usr/sbin/nologin tribuna
sudo mkdir -p /opt/tribuna
sudo tar -xzf tribuna-linux-amd64.tar.gz -C /opt/tribuna
sudo mkdir -p /opt/tribuna/data /opt/tribuna/frontend/uploads/news /opt/tribuna/frontend/uploads/ads
sudo chown -R tribuna:tribuna /opt/tribuna
sudo cp /opt/tribuna/deploy/tribuna.env.example /etc/tribuna.env
sudo chmod 640 /etc/tribuna.env
```

Проверьте пути в `/etc/tribuna.env`. Заполните `CONTACT_EMAIL` действующим контактом владельца. `ADMIN_SECURE_COOKIES=true` для HTTPS. Все Go-сервисы слушают только 127.0.0.1.

## Первый администратор

```bash
read -r -p 'Email администратора: ' TRIBUNA_ADMIN_EMAIL
read -r -s -p 'Пароль, минимум 12 символов: ' TRIBUNA_ADMIN_PASSWORD
printf '\n'
export TRIBUNA_ADMIN_PASSWORD
sudo --preserve-env=TRIBUNA_ADMIN_PASSWORD -u tribuna env DATA_DIR=/opt/tribuna/data /opt/tribuna/bin/trbn-admin create-user --email "$TRIBUNA_ADMIN_EMAIL"
unset TRIBUNA_ADMIN_PASSWORD
```

Учетных записей и паролей в архиве нет. Команду выполняйте в доверенном терминале сервера.

## Сервисы

```bash
sudo cp /opt/tribuna/deploy/trbn-*.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now trbn-ingester trbn-admin trbn-backend
```

## Домен и HTTPS

Направьте A/AAAA-записи своего домена на VPS, откройте 80/443. Скопируйте `deploy/nginx.conf` в `/etc/nginx/sites-available/tribuna`. Замените `DOMAIN` на настоящий домен, не оставляйте этот шаблон.

```bash
sudo ln -s /etc/nginx/sites-available/tribuna /etc/nginx/sites-enabled/tribuna
sudo nginx -t
sudo systemctl reload nginx
sudo certbot --nginx -d YOUR_DOMAIN
```

До получения HTTPS Secure-cookie не обеспечит вход по HTTP; после Certbot используйте `https://YOUR_DOMAIN/admin/`. Внешний доступ к 8080–8082 не нужен.

## Контроль

```bash
curl --fail http://127.0.0.1:8080/healthz
sudo systemctl status trbn-backend trbn-ingester trbn-admin
sudo journalctl -u trbn-ingester -n 50 --no-pager
```

Проверьте в браузере главную, фильтры, новость, `/sources`, вход и размещение тестового баннера. Отдельно проверьте HTTPS и адаптивность на реальном домене. Первая RSS-загрузка может занять десятки секунд.

## Эксплуатация

- Перезапускайте `trbn-backend` после изменения HTML-шаблонов. CSS/JS кэшируются до 5 минут; при обновлении меняйте версию в `index.html`.
- При изменении `sources.json` перезапустите `trbn-ingester`.
- Изображения RSS выключены по умолчанию. При включении `FETCH_IMAGES=true` следите за размером диска. Чистка удаляет только старые неиспользуемые файлы; используемые изображения не удаляются автоматически, чтобы не ломать архив.
- База новостей хранится постоянно; периодически контролируйте её размер и делайте копии через SQLite `.backup`.
- Для консистентной резервной копии БД: `sqlite3 /opt/tribuna/data/ingester.db ".backup '/PATH/TO/BACKUP/ingester.db'"`. Копируйте также `frontend/uploads/ads`, шаблоны и собственную конфигурацию; защищайте резервную копию с паролями-хешами.
- Не выполняйте `rm` по wildcard для очистки живой БД/папки uploads. При отказе источника последняя успешная лента остаётся доступной.
- Статусы источников показываются на `/sources`; код не обходит CAPTCHA, блокировки или ограничения источников.

## Обновление

Остановите сервисы, сохраните резервную копию, замените программы/статические файлы/шаблоны из нового архива, **сохранив** `data/ingester.db`, uploads и `/etc/tribuna.env`, затем запустите сервисы и повторите контроль. Автоматической миграции старого сайта TRBN нет: новый проект разворачивается самостоятельно.
