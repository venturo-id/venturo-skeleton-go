# Shared Infrastructure

Penambahan lima subsistem infrastruktur bersama (*shared infrastructure*) ke skeleton: **RabbitMQ**, **Sentry**, **Cache**, **Storage (S3/MinIO)**, dan **Notification (SMS/Push)**. Semua opsional — aplikasi tetap boot meski subsistem tidak dikonfigurasi atau tidak tersedia.

## Ringkasan

| Subsistem    | Lokasi                          | Perilaku bila tidak dikonfigurasi              | Status boot |
|--------------|---------------------------------|------------------------------------------------|-------------|
| RabbitMQ     | `internal/shared/rabbitmq/`     | Skip / warn, messaging tidak jalan             | Degraded (non-fatal) |
| Sentry       | `pkg/sentry/`                   | No-op (DSN kosong)                              | Degraded (non-fatal) |
| Cache        | `pkg/cache/`                    | Pakai ulang koneksi Redis yang ada             | Aktif (ikut Redis) |
| Storage      | `pkg/storage/`                  | `nil` bila bucket/kredensial kosong            | Degraded (non-fatal) |
| Notification | `pkg/notification/`             | No-op per channel bila kredensial kosong       | Aktif (channel degrade) |

> Prinsip: hanya **DB** dan **Redis** yang *load-bearing* (gagal = boot gagal). Lima subsistem ini *degraded* — gagal koneksi hanya warn, app lanjut.

## Prinsip Desain

- **`pkg/` tidak import `internal/config`.** Tiap paket di `pkg/` punya `Config` lokal sendiri (`pkg/sentry.Config`, `pkg/cache.Config`, dst). `cmd/api/main.go` & `internal/router/router.go` yang memetakan nilai dari `internal/config` ke struct lokal tersebut. Mengikuti pola `pkg/email.SMTPConfig`.
- **`internal/shared/` boleh import `internal/config`.** Hanya `rabbitmq` yang dipakai langsung sebagai `config.RabbitMQConfig`, meniru `internal/shared/redis`.
- **Degraded by default.** Tiap subsistem punya jalur no-op / nil sehingga skeleton bisa jalan tanpa akun pihak ketiga.
- **Belum di-wire ke modul bisnis.** `cacheService`, `storageClient`, `notifier`, `publisher` saat ini `_ =` (disiapkan untuk injeksi nanti).

---

## 1. RabbitMQ (`internal/shared/rabbitmq/`)

Client AMQP bersama dengan auto-reconnect.

- `client.go` — `Client` membungkus `*amqp.Connection` dengan loop reconnect (di-guard `RWMutex`). `New(ctx, cfg)` mendial + verifikasi koneksi via probe channel.
- `publisher.go` — `Publisher` untuk publish ke topic exchange, dengan publisher confirm + timeout (`RABBITMQ_PUBLISH_TIMEOUT`).
- `consumer.go` — `Consumer` dengan QoS prefetch, re-subscribe otomatis tiap reconnect.
- `topology.go` — `DeclareExchange` (topic, durable) & `DeclareQueue` (durable, idempotent + binding routing key).
- `consumer_example.go` — `RegisterExampleConsumer` sebagai bukti end-to-end.

**Wiring** (`router.go`): bila `RABBITMQ_ENABLED=false` → warn, skip. Bila enabled tapi broker tak terjangkau → warn, app lanjut tanpa messaging. Bila sukses → bangun publisher + jalankan example consumer.

**Config** (`config.RabbitMQConfig`, punya helper `GetURL()` mirip `DatabaseConfig.GetDSN()` — `RABBITMQ_URL` menang bila diisi, jika tidak dirakit dari komponen).

## 2. Sentry (`pkg/sentry/`)

Wrapper `github.com/getsentry/sentry-go`.

- `sentry.go` — `Init(cfg)` inisialisasi client global; no-op bila DSN kosong. Helper `CaptureMessage`, `Flush`.
- `zapcore.go` — `zapcore.Core` yang men-*tee* log level error ke Sentry, dipasang ke `pkg/logger`.

**Wiring**:
- `main.go` — `sentry.Init(...)` setelah `logger.Initialize` (perlu logger untuk attach error-tee core), `defer sentry.Flush(2s)`.
- `router.go` — middleware `sentrygin.New({Repanic: true})` dipasang sebelum `RecoveryWithZap` (hanya bila DSN ada). Endpoint dev `GET /debug/sentry-test` (hanya `development` + Sentry aktif) untuk uji end-to-end.

## 3. Cache (`pkg/cache/`)

Cache typed (nilai JSON) di atas Redis.

- `cache.go` — interface `Cache`: `Get`/`Set`/`Delete`/`GetOrSet`. `RedisCache` implementasinya.
- `noop.go` — implementasi no-op.
- `cache_test.go` — test pakai `miniredis`.

**Penting:** memakai ulang `*goredis.Client` yang sama dari `internal/shared/redis` — **tidak** buka koneksi baru. Hanya namespacing (`CACHE_KEY_PREFIX`) + TTL (`CACHE_DEFAULT_TTL`) yang dikonfigurasi di sini.

## 4. Storage (`pkg/storage/`)

Abstraksi storage multi-provider lewat `storage.Client` yang sudah ada (konsumen tetap provider-agnostic).

- `factory.go` — `New(ctx, cfg)` pilih backend: `gcs` (default) | `s3` | `minio`.
- `config.go` — `Config` lokal (GCS + S3/MinIO settings).
- `s3.go` — `NewS3Client` (AWS SDK v2). MinIO = S3-compatible (`S3_ENDPOINT` + `S3_USE_PATH_STYLE=true`).

**Wiring** (`router.go` → `initStorage`): return `nil` (degraded) bila bucket tidak diset, sehingga app tetap boot tanpa storage. GCS tetap dipisah (`GCSConfig`) untuk *backward compatibility*.

## 5. Notification (`pkg/notification/`)

Messaging ke user (email, SMS, push) di balik satu `Notifier`.

- `notification.go` — `Notifier`, `Config` (Twilio + FCM). Email pakai ulang `pkg/email`.
- `email_smtp.go` — adapter email.
- `sms_twilio.go` — SMS via Twilio.
- `push_fcm.go` — push via FCM (`FCM_CREDENTIALS_JSON` kosong = push mati).
- `noop.go` — adapter no-op.

> Catatan: berbeda dari `pkg/discord.Notifier` (itu alerting infra warn/error → Discord). `notification.Notifier` ini untuk notifikasi bisnis, sengaja dipisah.

**Wiring** (`router.go`): `notification.New(...)` selalu sukses; channel tanpa kredensial degrade ke no-op (adapter yang dipilih di-log saat boot).

---

## Step Implementasi (Wiring ke Modul Bisnis)

Saat ini semua subsistem sudah di-init di `router.go` tapi belum dipakai (`_ =`). Berikut langkah memakainya dari sebuah modul bisnis. Pola umum: ambil instance dari `Setup()`, suntik ke `Initialize()` modul, simpan di service, panggil di handler/service.

### Pola Umum Injeksi

1. Bangun instance di `internal/router/router.go` → `Setup()` (sudah ada).
2. Hapus `_ =`, teruskan ke `Initialize()` modul saat registrasi route.
3. Modul simpan dependensi di struct service.
4. Panggil dari service/handler.

---

### Step — RabbitMQ (Publish & Consume Event)

**1. Publish dari service.** Suntik `*rabbitmq.Publisher` ke service modul.

```go
// router.go — di blok RabbitMQ, ganti `_ = publisher`:
ordersModule := orders.Initialize(db, publisher)
ordersModule.SetupRoutes(coreV1)

// service modul:
type Service struct {
    publisher *rabbitmq.Publisher
}

func (s *Service) CreateOrder(ctx context.Context, o *domain.Order) error {
    // ... simpan order ...
    return s.publisher.Publish(ctx, "order.created", o) // routing key
}
```

**2. Consume.** Daftarkan handler sebelum `consumer.Start()`.

```go
// router.go — sebelum consumer.Start(...):
consumer.Register("orders.created", "order.created",
    func(ctx context.Context, d amqp.Delivery) error {
        // return nil = ack; return err = nack (requeue sekali, lalu drop)
        return ordersModule.HandleOrderCreated(ctx, d.Body)
    })
```

Catatan: queue & exchange dideklarasi otomatis (idempotent). Handler return error → nack dengan requeue sekali, gagal kedua → drop (anti poison-loop). Consumer re-subscribe sendiri tiap reconnect.

---

### Step — Cache (Cache-Aside)

`cacheService` sudah dibangun (reuse koneksi Redis). Suntik `cache.Cache` ke service.

```go
// router.go — ganti `_ = cacheService`:
usersModule := users.Initialize(db, cacheService)

// service — pola GetOrSet (cache-aside):
func (s *Service) GetProfile(ctx context.Context, id string) (*Profile, error) {
    var p Profile
    err := s.cache.GetOrSet(ctx, "profile:"+id, &p, 10*time.Minute,
        func() (any, error) {
            return s.repo.GetByID(ctx, id) // dipanggil hanya saat cache miss
        })
    return &p, err
}

// invalidasi saat update:
s.cache.Delete(ctx, "profile:"+id)
```

TTL `<= 0` pakai `CACHE_DEFAULT_TTL`. Key otomatis di-prefix `CACHE_KEY_PREFIX`.

---

### Step — Storage (Upload File)

`storageClient` bisa `nil` (degraded). **Selalu cek nil** sebelum pakai.

```go
// router.go — ganti `_ = storageClient`:
docsModule := documents.Initialize(db, storageClient)

// service:
func (s *Service) Upload(ctx context.Context, r io.Reader, path, mime string) (*storage.FileInfo, error) {
    if s.storage == nil {
        return nil, errors.New("storage not configured")
    }
    return s.storage.Upload(ctx, &storage.UploadInput{
        Reader: r, ObjectPath: path, ContentType: mime,
    })
}

// URL baca sementara:
url, _ := s.storage.SignedURL(ctx, path, 15*time.Minute)
```

Provider transparan (GCS/S3/MinIO) — konsumen tetap sama. Method: `Upload`, `Delete`, `SignedURL`, `PublicURL`.

---

### Step — Notification (Email/SMS/Push)

`notifier` selalu non-nil; channel tanpa kredensial = no-op (aman dipanggil).

```go
// router.go — ganti `_ = notifier`:
authModule := auth.Initialize(db, notifier)

// service — kirim multi-channel:
err := s.notifier.Notify(ctx, notification.Request{
    Channels:  []notification.Channel{notification.ChannelSMS, notification.ChannelPush},
    Phone:     user.Phone,
    PushToken: user.DeviceToken,
    Title:     "Login baru",
    Body:      "Akun Anda baru saja login.",
})
```

Catatan: channel email lewat `Notify` masih placeholder (template domain-specific). Untuk email pakai `notifier.Email` langsung (`pkg/email.EmailService`).

---

### Step — Sentry (Sudah Aktif)

Tidak perlu wiring per-modul. Saat `SENTRY_DSN` diisi:

- Panic otomatis ke-capture (middleware `sentrygin`).
- Semua `logger.Error(...)` ke-tee ke Sentry (via zapcore).

Capture manual bila perlu:

```go
import pkgsentry "venturo-skeleton-go/pkg/sentry"

pkgsentry.CaptureMessage("pembayaran gagal divalidasi")
// atau cukup:
logger.Error("pembayaran gagal", logger.Err(err)) // otomatis ke Sentry
```

Verifikasi (dev): `curl localhost:8080/debug/sentry-test`.

---

## Perubahan File Inti

- `internal/config/config.go` — tambah `RabbitMQConfig`, `SentryConfig`, `CacheConfig`, `StorageConfig`, `NotificationConfig` ke `Config`; loader env; helper `getEnvFloat64` & `RabbitMQConfig.GetURL()`.
- `cmd/api/main.go` — init + flush Sentry.
- `internal/router/router.go` — init Cache, Storage, Notification, RabbitMQ; middleware Sentry; helper `initStorage`; endpoint dev `/debug/sentry-test`.
- `.env.example` — variabel env semua subsistem.
- `go.mod` / `go.sum` — dependensi baru: `rabbitmq/amqp091-go`, `getsentry/sentry-go(+gin)`, `aws-sdk-go-v2` (S3), `twilio-go`, `alicebob/miniredis` (test).

## Variabel Environment Baru

```bash
# RabbitMQ (degraded — app tetap boot bila unreachable)
RABBITMQ_ENABLED=true
RABBITMQ_HOST=localhost
RABBITMQ_PORT=5672
RABBITMQ_USER=guest
RABBITMQ_PASSWORD=guest
RABBITMQ_VHOST=/
RABBITMQ_EXCHANGE=skeleton.events
RABBITMQ_RECONNECT_INTERVAL=5s
RABBITMQ_PREFETCH_COUNT=10
RABBITMQ_PUBLISH_TIMEOUT=5s
# RABBITMQ_URL=                  # override penuh, menang atas komponen di atas

# Sentry (no-op bila DSN kosong)
SENTRY_DSN=
SENTRY_ENVIRONMENT=             # default ke ENV
SENTRY_RELEASE=
SENTRY_TRACES_SAMPLE_RATE=0.0   # 0.0 = tracing mati; 0.1 = 10%

# Cache (pakai ulang koneksi Redis)
CACHE_KEY_PREFIX=cache
CACHE_DEFAULT_TTL=5m

# Storage — STORAGE_PROVIDER: gcs (default) | s3 | minio
STORAGE_PROVIDER=gcs
S3_ENDPOINT=                    # mis. http://localhost:9000 untuk MinIO
S3_REGION=us-east-1
S3_BUCKET=
S3_ACCESS_KEY_ID=
S3_SECRET_ACCESS_KEY=
S3_USE_PATH_STYLE=false         # true untuk MinIO
S3_PUBLIC_BASE_URL=             # opsional CDN/base URL

# Notification (no-op per channel bila kredensial kosong)
TWILIO_ACCOUNT_SID=
TWILIO_AUTH_TOKEN=
TWILIO_FROM_NUMBER=
FCM_CREDENTIALS_JSON=           # service-account JSON; kosong = push mati
```
