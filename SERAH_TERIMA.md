# Dokumen Serah Terima Sistem
## `ptdpn-eform-service` — Go Backend API

**PT BPR Daya Perdana Nusantara**
**Divisi IT — Sistem eForm Onboarding Digital**

---

| | |
|---|---|
| **Tanggal Serah Terima** | 27 Agustus 2026 |
| **Diserahkan oleh** | Tim Pengembang |
| **Diterima oleh** | Abdi — IT Section Head |
| **Repository** | https://github.com/cappyHoding/ptdpn-eform-service |
| **Branch Utama** | `master` |
| **Versi Go** | 1.25.0 |

---

## 1. Ringkasan Sistem

`ptdpn-eform-service` adalah backend API utama untuk sistem eForm Onboarding Digital BPR Perdana. Sistem ini menangani seluruh alur pendaftaran produk perbankan secara digital, mulai dari pengisian data nasabah, verifikasi identitas (eKYC), hingga penandatanganan kontrak elektronik (e-Sign).

---

## 2. Stack Teknologi

| Komponen | Teknologi | Versi |
|---|---|---|
| Bahasa | Go | 1.25.0 |
| HTTP Framework | Gin | v1.10.0 |
| ORM | GORM | v1.25.10 |
| Database | MySQL | 8.x |
| Cache / Session | Redis | v9 |
| Logging | Uber Zap | v1.27.0 |
| JWT Auth (Internal) | golang-jwt/jwt | v5 (RS256) |
| Config | Viper | v1.19.0 |
| PDF Generator | jung-kurt/gofpdf | v1.16.2 |
| Containerisasi | Docker + Docker Compose | - |

---

## 3. Struktur Direktori

```
ptdpn-eform-service/
├── cmd/server/           # Entry point (main.go) + dependency wiring
├── config/               # Struct konfigurasi (config.go), baca dari env
├── internal/
│   ├── api/
│   │   ├── handler/      # HTTP handlers (application, admin, auth, webhook)
│   │   ├── middleware/   # Auth, logger, recovery, rate limiter
│   │   └── router/       # Route registration (router.go)
│   ├── domain/           # Interface domain / business logic
│   ├── integration/
│   │   ├── vida/         # VIDA eKYC: OCR, Fraud, Direct Sign, eMeterai
│   │   └── ioh/          # IOH Tri — SMS OTP via SMPP
│   ├── model/            # GORM entity structs (models.go)
│   ├── repository/       # Database access layer
│   ├── service/          # Business logic layer
│   └── worker/           # Background workers (fraud poller, dll)
├── migrations/           # SQL migration files (sequential, 001–007)
├── keys/                 # RSA keypair untuk JWT (tidak di-commit ke Git)
├── assets/               # Template PDF, logo, dll
├── storage/              # File uploads (KTP, selfie, kontrak, agunan)
├── deploy/               # Konfigurasi deployment (systemd, nginx, dll)
├── pkg/                  # Shared utilities (jwt, logger, response)
├── scripts/              # Helper scripts (seed, migrate)
├── Dockerfile
├── docker-compose.yml
├── Makefile
├── .env.example          # Template konfigurasi — WAJIB dibaca sebelum deploy
└── go.mod / go.sum
```

---

## 4. Fitur yang Telah Diimplementasikan

### 4.1 Alur Customer (eForm Onboarding)

Sistem mendukung 4 jenis produk dengan wizard multi-langkah:

| Step | Deskripsi | Endpoint |
|---|---|---|
| 1 | Persetujuan Syarat & Ketentuan | `POST /api/v1/applications/agree` |
| 2 | Buat Aplikasi + dapat session token | `POST /api/v1/applications` |
| 3 | OCR KTP via VIDA | `POST /api/v1/applications/:id/ocr` |
| 4 | Konfirmasi Data Diri | `PATCH /api/v1/applications/:id/personal-info` |
| 5 | Verifikasi OTP (SMS) | `POST /api/v1/applications/:id/otp/send` + `/verify` |
| 6 | Liveness Check via VIDA SDK | `POST /api/v1/applications/:id/liveness` |
| 7 | Info Rekening / Disbursement | `PATCH /api/v1/applications/:id/disbursement` |
| 8 | Upload Agunan | `PATCH /api/v1/applications/:id/collateral` |
| 9 | Submit ke Review | `POST /api/v1/applications/:id/submit` |

**Produk yang Didukung:** `TABUNGAN`, `DEPOSITO`, `PINJAMAN`, `PENGKINIAN_DATA`

### 4.2 eKYC — Integrasi VIDA

| Layanan | Endpoint VIDA | Credential |
|---|---|---|
| OCR KTP | `POST /main/v2/services/ktp/recognition` | OCR (SSO) |
| Fraud Mitigation | `GET /main/v2/services/fraud/:id/status` | OCR (SSO) |
| Liveness | VIDA Web SDK | OCR (SSO) |
| Direct Sign (e-Sign) | `POST /core/external-api/rest/v1/envelope` | Direct Sign credential |
| eMeterai | `POST /stamp/api/...` | eMeterai credential |

> **PENTING:** OCR/Fraud dan Direct Sign menggunakan **endpoint token yang berbeda**. Jangan tukar-tukar credentials.

**Mock Mode:**
- `VIDA_FRAUD_MOCK=true` → skip fraud API, set status approved otomatis
- `VIDA_CONTRACT_MOCK=true` → skip Direct Sign, buat kontrak mock

### 4.3 Notifikasi SMS — Integrasi IOH Tri

- Kirim OTP via SMS ke nomor HP nasabah
- Kirim notifikasi status (approved / rejected / siap tanda tangan) via SMS
- Provider: **IOH Tri** — `https://smsapi.three.co.id:25000/sendsms`

### 4.4 Alur Admin (Dashboard Internal)

| Fitur | Role yang Diizinkan |
|---|---|
| Lihat list dan detail aplikasi | operator, supervisor, admin |
| Timeline & audit trail | operator, supervisor, admin |
| Buka aplikasi untuk review | operator, admin |
| Rekomendasikan (Maker) | operator, admin |
| Setujui (Checker) | supervisor, admin |
| Tolak aplikasi | supervisor, admin |
| Tambah catatan | operator, admin |
| Manajemen user internal | admin |
| Konfigurasi sistem | admin |
| Audit logs | admin, supervisor |
| Dashboard statistik | semua |
| Lihat foto KTP / Selfie | semua |

### 4.5 Manajemen Kontrak & e-Sign

- Generate PDF kontrak otomatis dari template
- Upload ke VIDA Direct Sign untuk penandatanganan elektronik
- Webhook VIDA (`POST /webhooks/vida`) → status `COMPLETED` → selesaikan kontrak + notifikasi SMS

### 4.6 Background Workers

- **Fraud Poller:** Polling VIDA setiap 30 menit untuk cek status fraud aplikasi yang masih pending (`fraud_status IN ('001','002')`)

### 4.7 State Machine Aplikasi

```
DRAFT
  └─→ SUBMITTED
        └─→ UNDER_REVIEW
              ├─→ RECOMMENDED
              │     └─→ APPROVED
              │           └─→ CONTRACT_SENT
              │                 └─→ COMPLETED
              └─→ REJECTED
  (KYC gagal) └─→ FRAUD_REJECTED
```

---

## 5. Database

### 5.1 Konfigurasi

| Parameter | Nilai Default |
|---|---|
| Host | `localhost` |
| Port | `3306` |
| Database | `bpr_perdana_eform` |
| Max Open Connections | 25 |
| Max Idle Connections | 10 |
| Connection Max Lifetime | 5 menit |

### 5.2 Tabel Utama

| Tabel | Keterangan |
|---|---|
| `users` | Internal staff (operator, supervisor, admin) |
| `refresh_tokens` | Refresh token JWT admin |
| `applications` | Core — data aplikasi nasabah |
| `personal_informations` | Hasil OCR KTP + koreksi nasabah |
| `disbursements` | Info rekening pencairan |
| `collaterals` | Data dan file agunan |
| `ekyc_results` | Hasil liveness & fraud check |
| `contracts` | Data kontrak e-Sign |
| `application_notes` | Catatan operator/supervisor |
| `application_events` | Audit trail perubahan status |
| `audit_logs` | Log semua aksi admin |
| `system_configs` | Konfigurasi dinamis sistem |

### 5.3 Menjalankan Migrasi

```bash
# Jalankan migration SQL secara berurutan (urutan penting!)
mysql -u <user> -p bpr_perdana_eform < migrations/001_init_auth.sql
mysql -u <user> -p bpr_perdana_eform < migrations/002_init_applications.sql
mysql -u <user> -p bpr_perdana_eform < migrations/003_init_ekyc_and_contracts.sql
mysql -u <user> -p bpr_perdana_eform < migrations/004_init_logs_and_events.sql
mysql -u <user> -p bpr_perdana_eform < migrations/005_init_system_config.sql
mysql -u <user> -p bpr_perdana_eform < migrations/006_add_esign_tos.sql
mysql -u <user> -p bpr_perdana_eform < migrations/007_add_email_verified.sql
```

---

## 6. Konfigurasi Environment

Salin `.env.example` menjadi `.env` dan isi semua nilai yang diperlukan:

```bash
cp .env.example .env
```

### Variabel Kritis

| Variabel | Keterangan |
|---|---|
| `DB_USER`, `DB_PASSWORD` | Kredensial MySQL |
| `SESSION_SECRET_KEY` | String random 32 karakter untuk customer session |
| `JWT_PRIVATE_KEY_PATH` | Path ke file `keys/private.pem` |
| `JWT_PUBLIC_KEY_PATH` | Path ke file `keys/public.pem` |
| `VIDA_OCR_CLIENT_ID`, `VIDA_OCR_SECRET_KEY` | Credential VIDA OCR/Fraud |
| `VIDA_DSIGN_BASE_URL`, `VIDA_DSIGN_CLIENT_ID`, `VIDA_DSIGN_SECRET_KEY` | Credential VIDA Direct Sign |
| `VIDA_EMETERAI_*` | Credential VIDA eMeterai |
| `CORS_ALLOWED_ORIGINS` | URL frontend yang diizinkan (pisah dengan koma) |

### Generate RSA Keypair (Wajib di Server Production)

```bash
mkdir -p keys
openssl genrsa -out keys/private.pem 2048
openssl rsa -in keys/private.pem -pubout -out keys/public.pem
```

---

## 7. Cara Menjalankan

### Development

```bash
go mod download
go run ./cmd/server
# atau: make run
```

### Production (Docker)

```bash
docker build -t ptdpn-eform-service .
docker-compose up -d
```

---

## 8. Endpoint & URL Penting

| Endpoint | URL |
|---|---|
| Health Check | `GET /health` |
| Customer API | `/api/v1/applications/...` |
| Admin API | `/api/v1/admin/...` |
| Webhook VIDA | `POST /webhooks/vida` |

### Integrasi Eksternal (Sandbox)

| Layanan | URL |
|---|---|
| VIDA OCR/Fraud | `https://services-sandbox.vida.id` |
| VIDA Direct Sign API | `https://sandbox-sign-api.np.vida.id` |
| IOH SMS | `https://smsapi.three.co.id:25000/sendsms` |

---

## 9. Keamanan

| Aspek | Implementasi |
|---|---|
| Auth Admin | JWT RS256 (access token 15m, refresh 24h) |
| Auth Customer | HMAC session token (TTL konfigurasi) |
| CORS | Whitelist per origin |
| Rate Limiting | Middleware Gin |
| Webhook | Verifikasi HMAC signature VIDA per request |
| Password | bcrypt hash |
| Secret | Tidak pernah di-commit ke Git |
| File KTP/Selfie | Akses hanya via API authenticated, bukan URL statis |

---

## 10. Hal-hal yang Perlu Diperhatikan (Gotcha)

1. **Trailing slash di `VIDA_DSIGN_BASE_URL`** — jangan ada trailing slash, akan menyebabkan 404
2. **Token Direct Sign** — gunakan `getDSToken()`, BUKAN `client.getAccessToken()`
3. **Goroutine dalam loop** — selalu capture `l := loader` sebelum goroutine
4. **`pdf.AddPage()`** — jangan pernah di-comment, PDF akan corrupt
5. **Mock mode** — saat `VIDA_FRAUD_MOCK=true`, `vida_request_id` berisi `appID` — ini normal
6. **File storage production** — mount volume persistent untuk `/var/app/storage`
7. **RSA keys** — file `keys/` tidak di-commit, harus di-generate ulang di server production

---

## 11. Item Pending / Perlu Perhatian

| Item | Status | Keterangan |
|---|---|---|
| Integrasi IOH SMS Production | BLOCKED | Menunggu akun production IOH |
| VIDA Direct Sign Production | BLOCKED | Menunggu approval VIDA untuk go-live |
| eMeterai Production | BLOCKED | Menunggu credentials production VIDA |
| Setup CI/CD Pipeline | TODO | GitHub Actions belum dikonfigurasi |
| Automated Testing | TODO | Unit & integration test perlu dilengkapi |

---

## 12. Referensi

| | |
|---|---|
| **Repository** | https://github.com/cappyHoding/ptdpn-eform-service |
| **VIDA Docs** | https://docs.vida.id |

---

*Dokumen ini dibuat pada 27 Agustus 2026 sebagai bagian dari proses serah terima sistem eForm Onboarding Digital PT BPR Daya Perdana Nusantara.*
