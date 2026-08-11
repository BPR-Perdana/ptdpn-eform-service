package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// ─── ROLES & INTERNAL USERS ──────────────────────────────────────────────────

// Role maps to the `roles` table.
type Role struct {
	ID          uint8  `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	Name        string `gorm:"column:name;type:enum('admin','supervisor','operator');uniqueIndex" json:"name"`
	Description string `gorm:"column:description;type:varchar(100)"  json:"description"`
	Base
}

func (Role) TableName() string { return "roles" }

// InternalUser maps to the `internal_users` table.
// This represents BPR Perdana staff (operators, supervisors, admins).
type InternalUser struct {
	ID        string  `gorm:"column:id;type:char(36);primaryKey"          json:"id"`
	Username  string  `gorm:"column:username;type:varchar(50);uniqueIndex" json:"username"`
	FullName  string  `gorm:"column:full_name;type:varchar(100)"           json:"full_name"`
	Email     string  `gorm:"column:email;type:varchar(100);uniqueIndex"   json:"email"`
	Password  string  `gorm:"column:password;type:varchar(255)"            json:"-"` // NEVER serialize password
	RoleID    uint8   `gorm:"column:role_id"                               json:"role_id"`
	IsActive  bool    `gorm:"column:is_active;default:1"                   json:"is_active"`
	CreatedBy *string `gorm:"column:created_by;type:char(36)"              json:"created_by,omitempty"`

	// Associations — GORM loads these when you use Preload()
	Role Role `gorm:"foreignKey:RoleID"    json:"role,omitempty"`
	Base
}

func (InternalUser) TableName() string { return "internal_users" }

// ─── CUSTOMERS & SESSIONS ────────────────────────────────────────────────────

// Customer maps to the `customers` table.
// PII (Personally Identifiable Information) — handle with extra care.
type Customer struct {
	ID                string     `gorm:"column:id;type:char(36);primaryKey"          json:"id"`
	NIK               *string    `gorm:"column:nik;type:varchar(16)"                 json:"nik,omitempty"`
	FullName          *string    `gorm:"column:full_name;type:varchar(100)"           json:"full_name,omitempty"`
	MothersMaidenName *string    `gorm:"column:mothers_maiden_name;type:varchar(100)" json:"mothers_maiden_name,omitempty"`
	CurrentAddress    *string    `gorm:"column:current_address;type:text"            json:"current_address,omitempty"`
	Occupation        *string    `gorm:"column:occupation;type:varchar(100)"         json:"occupation,omitempty"`
	WorkDuration      *string    `gorm:"column:work_duration;type:varchar(50)"       json:"work_duration,omitempty"`
	MonthlyIncome     *uint64    `gorm:"column:monthly_income"                       json:"monthly_income,omitempty"`
	Education         *string    `gorm:"column:education"                            json:"education,omitempty"`
	Email             *string    `gorm:"column:email;type:varchar(100)"              json:"email,omitempty"`
	EmailVerified     *bool      `gorm:"column:email_verified;default:false"         json:"email_verified"`
	EmailVerifiedAt   *time.Time `gorm:"column:email_verified_at"                    json:"email_verified_at,omitempty"`
	PhoneNumber       *string    `gorm:"column:phone_number;type:varchar(20)"        json:"phone_number,omitempty"`
	PhoneNumberWA     *string    `gorm:"column:phone_number_wa;type:varchar(20)"     json:"phone_number_wa,omitempty"`
	PhoneVerified     *bool      `gorm:"column:phone_verified;default:false"         json:"phone_verified"`
	PhoneVerifiedAt   *time.Time `gorm:"column:phone_verified_at"                    json:"phone_verified_at,omitempty"`
	WorkAddress       *string    `gorm:"column:work_address;type:text"               json:"work_address,omitempty"`
	Base
}

func (Customer) TableName() string { return "customers" }

// CustomerSession maps to the `customer_sessions` table.
// Tracks anonymous sessions for customers filling out the form.
// NOTE: No Base embed — sessions only need created_at, no updated_at or deleted_at.
type CustomerSession struct {
	ID            string     `gorm:"column:id;type:char(36);primaryKey"   json:"id"`
	ApplicationID string     `gorm:"column:application_id;type:char(36)"  json:"application_id"`
	Token         string     `gorm:"column:token;type:varchar(512)"       json:"-"`
	TokenHash     string     `gorm:"column:token_hash;type:char(64)"      json:"-"`
	IPAddress     *string    `gorm:"column:ip_address;type:varchar(45)"   json:"ip_address,omitempty"`
	UserAgent     *string    `gorm:"column:user_agent;type:text"          json:"user_agent,omitempty"`
	ExpiresAt     time.Time  `gorm:"column:expires_at"                    json:"expires_at"`
	RevokedAt     *time.Time `gorm:"column:revoked_at"                    json:"revoked_at,omitempty"`
	CreatedAt     time.Time  `gorm:"column:created_at;autoCreateTime"     json:"created_at"`
}

func (CustomerSession) TableName() string { return "customer_sessions" }

// ─── APPLICATIONS ────────────────────────────────────────────────────────────

// ApplicationStatus represents the state machine statuses.
// Using typed constants prevents typos and enables IDE autocompletion.
type ApplicationStatus string

const (
	StatusDraft         ApplicationStatus = "DRAFT"
	StatusPendingReview ApplicationStatus = "PENDING_REVIEW"
	StatusInReview      ApplicationStatus = "IN_REVIEW"
	StatusRecommended   ApplicationStatus = "RECOMMENDED"
	StatusFraudRejected ApplicationStatus = "FRAUD_REJECTED"
	StatusApproved      ApplicationStatus = "APPROVED"
	StatusRejected      ApplicationStatus = "REJECTED"
	StatusSigning       ApplicationStatus = "SIGNING"
	StatusCompleted     ApplicationStatus = "COMPLETED"
	StatusExpired       ApplicationStatus = "EXPIRED"
)

// ProductType represents the three onboarding product categories.
type ProductType string

const (
	ProductSaving  ProductType = "SAVING"
	ProductDeposit ProductType = "DEPOSIT"
	ProductLoan    ProductType = "LOAN"
)

// Application maps to the `applications` table.
// This is the core record that links all other tables together.
type Application struct {
	ID                  string            `gorm:"column:id;type:char(36);primaryKey"             json:"id"`
	CustomerID          string            `gorm:"column:customer_id;type:char(36)"               json:"customer_id"`
	ProductType         ProductType       `gorm:"column:product_type"                            json:"product_type"`
	Status              ApplicationStatus `gorm:"column:status;default:DRAFT"                    json:"status"`
	CurrentStep         uint8             `gorm:"column:current_step;default:1"                  json:"current_step"`
	LastStepCompleted   uint8             `gorm:"column:last_step_completed;default:0"           json:"last_step_completed"`
	AgreementAccepted   bool              `gorm:"column:agreement_accepted;default:0"            json:"agreement_accepted"`
	AgreementAcceptedAt *time.Time        `gorm:"column:agreement_accepted_at"                   json:"agreement_accepted_at,omitempty"`
	AgreementIP         *string           `gorm:"column:agreement_ip;type:varchar(45)"           json:"agreement_ip,omitempty"`
	AgreementUserAgent  *string           `gorm:"column:agreement_user_agent;type:text"          json:"-"` // internal only
	SubmittedAt         *time.Time        `gorm:"column:submitted_at"                            json:"submitted_at,omitempty"`
	RejectionReason     *string           `gorm:"column:rejection_reason;type:text"              json:"rejection_reason,omitempty"`
	CompletedAt         *time.Time        `gorm:"column:completed_at"                            json:"completed_at,omitempty"`
	SignDeadline        *time.Time        `gorm:"column:sign_deadline"                           json:"sign_deadline,omitempty"`

	// Associations
	Customer         Customer          `gorm:"foreignKey:CustomerID"   json:"customer,omitempty"`
	SavingDetail     *SavingDetail     `gorm:"foreignKey:ApplicationID" json:"saving_detail,omitempty"`
	DepositDetail    *DepositDetail    `gorm:"foreignKey:ApplicationID" json:"deposit_detail,omitempty"`
	LoanDetail       *LoanDetail       `gorm:"foreignKey:ApplicationID" json:"loan_detail,omitempty"`
	CollateralItems  []CollateralItem  `gorm:"foreignKey:ApplicationID" json:"collateral_items,omitempty"`
	DisbursementData *DisbursementData `gorm:"foreignKey:ApplicationID" json:"disbursement_data,omitempty"`
	OCRResult        *OCRResult        `gorm:"foreignKey:ApplicationID" json:"ocr_result,omitempty"`
	LivenessResult   *LivenessResult   `gorm:"foreignKey:ApplicationID" json:"liveness_result,omitempty"`
	ContractDocument *ContractDocument `gorm:"foreignKey:ApplicationID" json:"contract_document,omitempty"`
	PaymentProofPath *string           `gorm:"column:payment_proof_path" json:"payment_proof_path,omitempty"`
	PaymentProofAt   *time.Time        `gorm:"column:payment_proof_at"   json:"payment_proof_at,omitempty"`
	Base
}

func (Application) TableName() string { return "applications" }

// ─── PRODUCT DETAILS ─────────────────────────────────────────────────────────

// SavingDetail maps to the `saving_details` table.
type SavingDetail struct {
	ApplicationID  string `gorm:"column:application_id;type:char(36);primaryKey" json:"application_id"`
	ProductName    string `gorm:"column:product_name"                            json:"product_name"`
	InitialDeposit uint64 `gorm:"column:initial_deposit"                         json:"initial_deposit"`
	SourceOfFunds  string `gorm:"column:source_of_funds;type:varchar(100)"       json:"source_of_funds"`
	SavingPurpose  string `gorm:"column:saving_purpose;type:varchar(200)"        json:"saving_purpose"`
	Base
}

func (SavingDetail) TableName() string { return "saving_details" }

// DepositDetail maps to the `deposit_details` table.
type DepositDetail struct {
	ApplicationID     string   `gorm:"column:application_id;type:char(36);primaryKey" json:"application_id"`
	ProductName       string   `gorm:"column:product_name;type:varchar(100)"          json:"product_name"`
	PlacementAmount   uint64   `gorm:"column:placement_amount"                        json:"placement_amount"`
	TenorMonths       uint8    `gorm:"column:tenor_months"                            json:"tenor_months"`
	RolloverType      string   `gorm:"column:rollover_type"                           json:"rollover_type"`
	InterestRate      *float64 `gorm:"column:interest_rate;type:decimal(5,2)"         json:"interest_rate,omitempty"`
	SourceOfFunds     string   `gorm:"column:source_of_funds"                         json:"source_of_funds"`
	InvestmentPurpose *string  `gorm:"column:investment_purpose;type:varchar(200)"    json:"investment_purpose,omitempty"`
	Base
}

func (DepositDetail) TableName() string { return "deposit_details" }

// LoanDetail maps to the `loan_details` table.
type LoanDetail struct {
	ApplicationID   string   `gorm:"column:application_id;type:char(36);primaryKey" json:"application_id"`
	ProductName     string   `gorm:"column:product_name"                            json:"product_name"`
	RequestedAmount uint64   `gorm:"column:requested_amount"                        json:"requested_amount"`
	TenorMonths     uint8    `gorm:"column:tenor_months"                            json:"tenor_months"`
	InterestRate    *float64 `gorm:"column:interest_rate;type:decimal(5,2)"         json:"interest_rate,omitempty"`
	LoanPurpose     string   `gorm:"column:loan_purpose;type:text"                  json:"loan_purpose"`
	PaymentSource   string   `gorm:"column:payment_source;type:varchar(200)"        json:"payment_source"`
	SourceOfFunds   string   `gorm:"column:source_of_funds"                         json:"source_of_funds"`
	Base
}

func (LoanDetail) TableName() string { return "loan_details" }

// CollateralItem maps to the `collateral_items` table.
type CollateralItem struct {
	ID             string  `gorm:"column:id;type:char(36);primaryKey"             json:"id"`
	ApplicationID  string  `gorm:"column:application_id;type:char(36)"            json:"application_id"`
	CollateralType string  `gorm:"column:collateral_type"                         json:"collateral_type"`
	Description    *string `gorm:"column:description;type:text"                   json:"description,omitempty"`
	EstimatedValue *uint64 `gorm:"column:estimated_value"                         json:"estimated_value,omitempty"`
	AttachmentPath *string `gorm:"column:attachment_path;type:varchar(500)"       json:"attachment_path,omitempty"`
	SortOrder      uint8   `gorm:"column:sort_order;default:1"                    json:"sort_order"`
	Base
}

func (CollateralItem) TableName() string { return "collateral_items" }

// ─── DISBURSEMENT ─────────────────────────────────────────────────────────────

// DisbursementData maps to the `disbursement_data` table.
type DisbursementData struct {
	ID            string `gorm:"column:id;type:char(36);primaryKey"          json:"id"`
	ApplicationID string `gorm:"column:application_id;type:char(36)"         json:"application_id"`
	BankName      string `gorm:"column:bank_name;type:varchar(100)"          json:"bank_name"`
	BankCode      string `gorm:"column:bank_code;type:varchar(10)"           json:"bank_code"`
	AccountNumber string `gorm:"column:account_number;type:varchar(30)"      json:"account_number"`
	AccountHolder string `gorm:"column:account_holder;type:varchar(100)"     json:"account_holder"`
	Base
}

func (DisbursementData) TableName() string { return "disbursement_data" }

// ─── eKYC RESULTS ────────────────────────────────────────────────────────────

// JSON is a helper type that allows storing arbitrary JSON in a database column.
// GORM doesn't natively support map[string]interface{} for JSON columns.
type JSON map[string]interface{}

// Value implements the driver.Valuer interface — converts Go value → SQL value.
func (j JSON) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	return json.Marshal(j)
}

// Scan implements the sql.Scanner interface — converts SQL value → Go value.
func (j *JSON) Scan(value interface{}) error {
	if value == nil {
		*j = nil
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("failed to scan JSON: expected []byte, got %T", value)
	}
	return json.Unmarshal(bytes, j)
}

// OCRResult maps to the `ocr_results` table.
// Stores extracted KTP data from VIDA OCR API call.
type OCRResult struct {
	ID              string   `gorm:"column:id;type:char(36);primaryKey"             json:"id"`
	ApplicationID   string   `gorm:"column:application_id;type:char(36)"            json:"application_id"`
	VidaRequestID   string   `gorm:"column:vida_request_id;type:varchar(100)"       json:"vida_request_id"`
	RawResponse     JSON     `gorm:"column:raw_response;type:json"                  json:"-"` // internal only
	NIK             *string  `gorm:"column:nik;type:varchar(16)"                    json:"nik,omitempty"`
	FullName        *string  `gorm:"column:full_name;type:varchar(100)"             json:"full_name,omitempty"`
	BirthPlace      *string  `gorm:"column:birth_place;type:varchar(100)"           json:"birth_place,omitempty"`
	BirthDate       *string  `gorm:"column:birth_date;type:date"                    json:"birth_date,omitempty"`
	Gender          *string  `gorm:"column:gender"                                  json:"gender,omitempty"`
	Address         *string  `gorm:"column:address;type:text"                       json:"address,omitempty"`
	RTRW            *string  `gorm:"column:rt_rw;type:varchar(10)"                  json:"rt_rw,omitempty"`
	Kelurahan       *string  `gorm:"column:kelurahan;type:varchar(100)"             json:"kelurahan,omitempty"`
	Kecamatan       *string  `gorm:"column:kecamatan;type:varchar(100)"             json:"kecamatan,omitempty"`
	KabupatenKota   *string  `gorm:"column:kabupaten_kota;type:varchar(100)"        json:"kabupaten_kota,omitempty"`
	Provinsi        *string  `gorm:"column:provinsi;type:varchar(100)"              json:"provinsi,omitempty"`
	Religion        *string  `gorm:"column:religion"                                json:"religion,omitempty"`
	MaritalStatus   *string  `gorm:"column:marital_status"                          json:"marital_status,omitempty"`
	Occupation      *string  `gorm:"column:occupation;type:varchar(100)"            json:"occupation,omitempty"`
	Nationality     *string  `gorm:"column:nationality;type:varchar(50)"            json:"nationality,omitempty"`
	ExpiryDate      *string  `gorm:"column:expiry_date;type:date"                   json:"expiry_date,omitempty"`
	ConfidenceScore *float64 `gorm:"column:confidence_score;type:decimal(5,4)"      json:"confidence_score,omitempty"`
	NIKConfidence   *float64 `gorm:"column:nik_confidence;type:decimal(5,4)"        json:"nik_confidence,omitempty"`
	NameConfidence  *float64 `gorm:"column:name_confidence;type:decimal(5,4)"       json:"name_confidence,omitempty"`
	KTPImagePath    string   `gorm:"column:ktp_image_path;type:varchar(500)"        json:"-"` // internal path
	Base
}

func (OCRResult) TableName() string { return "ocr_results" }

// LivenessResult maps to the `liveness_results` table.
// Stores facial liveness and face-match results from VIDA.
type LivenessResult struct {
	ID              string   `gorm:"column:id;type:char(36);primaryKey"             json:"id"`
	ApplicationID   string   `gorm:"column:application_id;type:char(36)"            json:"application_id"`
	VidaRequestID   string   `gorm:"column:vida_request_id;type:varchar(100)"       json:"vida_request_id"`
	VidaSessionID   *string  `gorm:"column:vida_session_id;type:varchar(100)"       json:"vida_session_id,omitempty"`
	RawResponse     JSON     `gorm:"column:raw_response;type:json"                  json:"-"`
	LivenessStatus  string   `gorm:"column:liveness_status"                         json:"liveness_status"`
	LivenessScore   *float64 `gorm:"column:liveness_score;type:decimal(5,4)"        json:"liveness_score,omitempty"`
	FaceMatchStatus *string  `gorm:"column:face_match_status"                       json:"face_match_status,omitempty"`
	FaceMatchScore  *float64 `gorm:"column:face_match_score;type:decimal(5,4)"      json:"face_match_score,omitempty"`
	// ── Fraud status polling fields ───────────────────────────────────────────
	FraudStatus     string  `gorm:"column:fraud_status;default:001"                json:"fraud_status"`
	KYCEventID      *string `gorm:"column:kyc_event_id;type:varchar(100)"          json:"kyc_event_id,omitempty"`
	SelfieImagePath *string `gorm:"column:selfie_image_path;type:varchar(500)"     json:"-"`
	Base
}

func (LivenessResult) TableName() string { return "liveness_results" }

// ─── REVIEW ACTIONS ──────────────────────────────────────────────────────────

// ReviewAction maps to the `review_actions` table.
// Every maker-checker action is permanently recorded here.
// NOTE: No Base embed — review_actions is append-only (no updated_at, no deleted_at).
type ReviewAction struct {
	ID            string    `gorm:"column:id;type:char(36);primaryKey"     json:"id"`
	ApplicationID string    `gorm:"column:application_id;type:char(36)"    json:"application_id"`
	ActorID       string    `gorm:"column:actor_id;type:char(36)"          json:"actor_id"`
	ActorUsername string    `gorm:"column:actor_username;type:varchar(50)" json:"actor_username"`
	ActorRole     string    `gorm:"column:actor_role"                      json:"actor_role"`
	Action        string    `gorm:"column:action"                          json:"action"`
	Notes         *string   `gorm:"column:notes;type:text"                 json:"notes,omitempty"`
	CreatedAt     time.Time `gorm:"column:created_at;autoCreateTime"       json:"created_at"`

	// Association
	Actor InternalUser `gorm:"foreignKey:ActorID" json:"actor,omitempty"`
}

func (ReviewAction) TableName() string { return "review_actions" }

// ─── CONTRACT DOCUMENTS ──────────────────────────────────────────────────────

// ContractDocument maps to the `contract_documents` table.
type ContractDocument struct {
	ID                string     `gorm:"column:id;type:char(36);primaryKey"          json:"id"`
	ApplicationID     string     `gorm:"column:application_id;type:char(36)"         json:"application_id"`
	DocumentType      string     `gorm:"column:document_type"                        json:"document_type"`
	FilePath          string     `gorm:"column:file_path;type:varchar(500)"          json:"-"` // internal path
	FileSizeBytes     *uint32    `gorm:"column:file_size_bytes"                      json:"file_size_bytes,omitempty"`
	FileHashSHA256    *string    `gorm:"column:file_hash_sha256;type:char(64)"       json:"file_hash_sha256,omitempty"`
	VidaDocumentID    *string    `gorm:"column:vida_document_id;type:varchar(100)"   json:"vida_document_id,omitempty"`
	VidaSignRequestID *string    `gorm:"column:vida_sign_request_id;type:varchar(100)" json:"vida_sign_request_id,omitempty"`
	EMateraiID        *string    `gorm:"column:emeterai_id;type:varchar(100)"        json:"emeterai_id,omitempty"`
	EMateraiAppliedAt *time.Time `gorm:"column:emeterai_applied_at"                  json:"emeterai_applied_at,omitempty"`
	SignStatus        string     `gorm:"column:sign_status;default:PENDING"          json:"sign_status"`
	SignLink          *string    `gorm:"column:sign_link;type:varchar(500)"          json:"sign_link,omitempty"`
	SignLinkSentAt    *time.Time `gorm:"column:sign_link_sent_at"                    json:"sign_link_sent_at,omitempty"`
	SignDeadline      *time.Time `gorm:"column:sign_deadline"                        json:"sign_deadline,omitempty"`
	SignedAt            *time.Time `gorm:"column:signed_at"                              json:"signed_at,omitempty"`
	SignedFilePath      *string    `gorm:"column:signed_file_path;type:varchar(500)"     json:"-"`
	// ── eSign TOS acceptance (agreement page sebelum redirect ke VIDA) ────────
	ESignTOSAccepted   bool       `gorm:"column:esign_tos_accepted;default:0"           json:"esign_tos_accepted"`
	ESignTOSAcceptedAt *time.Time `gorm:"column:esign_tos_accepted_at"                  json:"esign_tos_accepted_at,omitempty"`
	ESignTOSIP         *string    `gorm:"column:esign_tos_ip;type:varchar(45)"          json:"-"` // internal audit
	GeneratedAt        time.Time  `gorm:"column:generated_at;autoCreateTime"            json:"generated_at"`
	Base
}

func (ContractDocument) TableName() string { return "contract_documents" }

// ─── WEBHOOK EVENTS ──────────────────────────────────────────────────────────

// WebhookEvent maps to the `webhook_events` table.
type WebhookEvent struct {
	ID              string     `gorm:"column:id;type:char(36);primaryKey"           json:"id"`
	Source          string     `gorm:"column:source;type:varchar(50);default:vida"  json:"source"`
	EventType       string     `gorm:"column:event_type;type:varchar(100)"          json:"event_type"`
	ApplicationID   *string    `gorm:"column:application_id;type:char(36)"          json:"application_id,omitempty"`
	VidaEventID     *string    `gorm:"column:vida_event_id;type:varchar(100)"       json:"vida_event_id,omitempty"`
	Payload         JSON       `gorm:"column:payload;type:json"                     json:"-"`
	Processed       bool       `gorm:"column:processed;default:0"                   json:"processed"`
	ProcessedAt     *time.Time `gorm:"column:processed_at"                          json:"processed_at,omitempty"`
	ProcessAttempts uint8      `gorm:"column:process_attempts;default:0"            json:"process_attempts"`
	ErrorMessage    *string    `gorm:"column:error_message;type:text"               json:"error_message,omitempty"`
	ReceivedAt      time.Time  `gorm:"column:received_at;autoCreateTime"            json:"received_at"`
}

func (WebhookEvent) TableName() string { return "webhook_events" }

// ─── NOTIFICATION LOGS ───────────────────────────────────────────────────────

// NotificationLog maps to the `notification_logs` table.
type NotificationLog struct {
	ID                string     `gorm:"column:id;type:char(36);primaryKey"               json:"id"`
	ApplicationID     string     `gorm:"column:application_id;type:char(36)"              json:"application_id"`
	Channel           string     `gorm:"column:channel"                                   json:"channel"`
	Recipient         string     `gorm:"column:recipient;type:varchar(200)"               json:"recipient"`
	Template          string     `gorm:"column:template;type:varchar(100)"                json:"template"`
	Subject           *string    `gorm:"column:subject;type:varchar(255)"                 json:"subject,omitempty"`
	Status            string     `gorm:"column:status;default:PENDING"                    json:"status"`
	ProviderMessageID *string    `gorm:"column:provider_message_id;type:varchar(200)"     json:"provider_message_id,omitempty"`
	SentAt            *time.Time `gorm:"column:sent_at"                                   json:"sent_at,omitempty"`
	ErrorMessage      *string    `gorm:"column:error_message;type:text"                   json:"error_message,omitempty"`
	RetryCount        uint8      `gorm:"column:retry_count;default:0"                     json:"retry_count"`
	Base
}

func (NotificationLog) TableName() string { return "notification_logs" }

// ─── AUDIT LOG ───────────────────────────────────────────────────────────────

// AuditLog maps to the `audit_logs` table.
// IMMUTABLE — no UpdatedAt, no DeletedAt, no soft delete.
type AuditLog struct {
	ID            uint64    `gorm:"column:id;primaryKey;autoIncrement"            json:"id"`
	ActorType     string    `gorm:"column:actor_type"                             json:"actor_type"`
	ActorID       *string   `gorm:"column:actor_id;type:varchar(36)"              json:"actor_id,omitempty"`
	ActorUsername *string   `gorm:"column:actor_username;type:varchar(100)"       json:"actor_username,omitempty"`
	ActorRole     *string   `gorm:"column:actor_role;type:varchar(20)"            json:"actor_role,omitempty"`
	Action        string    `gorm:"column:action;type:varchar(100)"               json:"action"`
	Description   *string   `gorm:"column:description;type:text"                  json:"description,omitempty"`
	EntityType    *string   `gorm:"column:entity_type;type:varchar(50)"           json:"entity_type,omitempty"`
	EntityID      *string   `gorm:"column:entity_id;type:varchar(36)"             json:"entity_id,omitempty"`
	OldValue      JSON      `gorm:"column:old_value;type:json"                    json:"old_value,omitempty"`
	NewValue      JSON      `gorm:"column:new_value;type:json"                    json:"new_value,omitempty"`
	IPAddress     *string   `gorm:"column:ip_address;type:varchar(45)"            json:"ip_address,omitempty"`
	UserAgent     *string   `gorm:"column:user_agent;type:text"                   json:"-"`
	RequestID     *string   `gorm:"column:request_id;type:varchar(100)"           json:"request_id,omitempty"`
	CreatedAt     time.Time `gorm:"column:created_at;autoCreateTime"            json:"created_at"`
	// NO UpdatedAt. NO DeletedAt. This table is append-only.
}

func (AuditLog) TableName() string { return "audit_logs" }

// ─── SYSTEM CONFIG ───────────────────────────────────────────────────────────

// SystemConfig maps to the `system_config` table.
type SystemConfig struct {
	ConfigKey   string     `gorm:"column:config_key;type:varchar(100);primaryKey" json:"config_key"`
	ConfigValue string     `gorm:"column:config_value;type:text"                  json:"config_value"`
	Description *string    `gorm:"column:description;type:varchar(255)"           json:"description,omitempty"`
	IsPublic    bool       `gorm:"column:is_public;default:0"                     json:"is_public"`
	UpdatedBy   *string    `gorm:"column:updated_by;type:char(36)"                json:"updated_by,omitempty"`
	UpdatedAt   time.Time  `gorm:"column:updated_at;autoUpdateTime"             json:"updated_at"`
	DeletedAt   *time.Time `gorm:"column:deleted_at;index"                     json:"deleted_at,omitempty"`
}

func (SystemConfig) TableName() string { return "system_config" }
