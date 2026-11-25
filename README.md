# Аудиосервис

Домашний сервер для личных медиатек MP3. Клиенты для macOS 14+ и Android 8+ умеют перемешивать всю медиатеку, воспроизводить выбранный плейлист и скачивать трек при лайке. Раздел «Скачанные» работает при недоступном сервере. На Android очередь продолжает играть после блокировки экрана.

Откройте меню с именем аккаунта → «Настройки» → «Скачивать понравившиеся треки на это устройство». По умолчанию автоскачивание включено. Выбор запоминается отдельно для каждого аккаунта на каждом устройстве. Он действует на новые лайки. Уже скачанная музыка остаётся доступной офлайн.

## Сервер

Понадобятся Go 1.26 и PostgreSQL 18. Подготовьте отдельную базу и роль, которой принадлежат объекты схемы сервиса. Передайте строку подключения в переменной `AUDIOSERVICE_DATABASE_URL`. Для сетевого подключения к PostgreSQL настройте TLS согласно конфигурации сервера базы.

```sh
go build -o dist/audioservice ./cmd/audioservice
export AUDIOSERVICE_DATABASE_URL='postgres://audio:password@127.0.0.1:5432/audio?sslmode=disable'
dist/audioservice migrate
dist/audioservice user create --name 'Мой аккаунт'
dist/audioservice serve --data-dir ./data
```

Замените значения в примере на реквизиты вашей локальной базы. `user create` печатает личный ключ. В приложении укажите адрес и ключ своего аккаунта. Повторите создание аккаунта для каждого слушателя.

По умолчанию сервер доступен на Mac по `http://localhost:8080`. Для Android-эмулятора или подключённого по USB Android используйте `adb reverse tcp:8080 tcp:8080` и тот же адрес в приложении.

### Домашняя сеть

Настройте сертификат с SAN для имени или IP, по которому клиенты будут обращаться к серверу. Передайте PEM-файлы цепочки и закрытого ключа.

```sh
dist/audioservice serve --listen 0.0.0.0:8080 --data-dir ./data \
  --tls-cert /path/to/fullchain.pem --tls-key /path/to/key.pem
```

Введите в приложении `https://<имя-или-IP>:8080`. Для собственного центра сертификации установите доверенный корневой сертификат на клиентских устройствах. На macOS разрешите приложению доступ к локальной сети, если система покажет запрос.

Для резервной копии остановите сервис и сохраните согласованные снимки PostgreSQL и каталога `data/audio`. При восстановлении используйте каталог аудио и копию базы с одного момента времени.

## Сборка клиентов

На macOS запускайте сборку из корня репозитория с установленными Command Line Tools.

```sh
./macos/build-app.sh
open dist/AudioService.app
```

Для Android укажите JDK 17 и каталог Android SDK с платформой API 36 и Build Tools 35.0.0.

```sh
export JAVA_HOME='/path/to/jdk-17'
export ANDROID_HOME='/path/to/android-sdk'
./android/gradlew -p android :app:lint :app:assembleDebug
adb install android/app/build/outputs/apk/debug/app-debug.apk
```

APK подписывается локальным debug-ключом. Для обновления установленного APK используйте тот же ключ подписи.

## API

Все маршруты находятся под `/api/v1`. Для запросов используйте заголовок `Authorization: Bearer <ключ>`. Списки возвращаются в поле `items`.

```sh
# URL сервера и личный ключ уже заданы в AUDIO_BASE и AUDIO_KEY.
curl --fail-with-body "$AUDIO_BASE/api/v1/playlists" \
  -H "Authorization: Bearer $AUDIO_KEY" \
  -H 'Content-Type: application/json' \
  --data '{"name":"Дорога"}'

# PLAYLIST_ID содержит id из ответа. Параметр playlist_id можно опустить.
curl --fail-with-body "$AUDIO_BASE/api/v1/tracks?title=Demo&playlist_id=$PLAYLIST_ID" \
  -H "Authorization: Bearer $AUDIO_KEY" \
  -H 'Content-Type: audio/mpeg' \
  --data-binary @song.mp3
```

- `GET /me` возвращает текущий аккаунт.
- `GET /playlists` и `POST /playlists` читают список и создают плейлист. `PATCH /playlists/{id}` меняет `name`, `DELETE /playlists/{id}` удаляет плейлист.
- `GET /playlists/{id}/tracks` возвращает треки в порядке добавления. `PUT` и `DELETE /playlists/{id}/tracks/{track_id}` добавляют и убирают связь с существующим треком.
- `GET /tracks` возвращает личную медиатеку. `POST /tracks` принимает MP3 до 64 МиБ. `PATCH /tracks/{id}` меняет `title`, `DELETE /tracks/{id}` удаляет трек и его связи.
- `PUT /tracks/{id}/like` ставит лайк, `DELETE /tracks/{id}/like` снимает. При включённом автоскачивании клиент сохраняет MP3 на устройстве, где поставили лайк.
- `GET /media/{track_id}` отдаёт MP3 и поддерживает `Range`. `HEAD` возвращает заголовки файла.

Трек можно включить в несколько плейлистов. Удаление из плейлиста сохраняет его в медиатеке. Сохранённую локальную копию можно удалить отдельно в «Скачанных».

## Локальные проверки

```sh
# AUDIOSERVICE_TEST_DATABASE_URL указывает на отдельный тестовый PostgreSQL.
go test ./...
go vet ./...
```
