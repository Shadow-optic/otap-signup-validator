package models

import (
	"fmt"
	"time"
)

// Wavelength represents an ITU-T C-band grid wavelength
type Wavelength struct {
	LambdaNm   float64 `json:"lambda_nm"`
	ChannelNum int32   `json:"channel_num"`
	Band       Band    `json:"band"`
	GridGHz    float64 `json:"grid_ghz"`
}

// ToKey returns a unique string key for this wavelength
func (w *Wavelength) ToKey() string {
	return fmt.Sprintf("%.2f:%d:%s:%.1f", w.LambdaNm, w.ChannelNum, w.Band.String(), w.GridGHz)
}

// String implements fmt.Stringer
func (w *Wavelength) String() string {
	return fmt.Sprintf("%.2fnm[ch%d,%s,%.1fGHz]", w.LambdaNm, w.ChannelNum, w.Band.String(), w.GridGHz)
}

type Band int32

const (
	BandUnspecified Band = 0
	BandCBand       Band = 1
	BandLBand       Band = 2
	BandSBand       Band = 3
)

func (b Band) String() string {
	switch b {
	case BandCBand:
		return "C_BAND"
	case BandLBand:
		return "L_BAND"
	case BandSBand:
		return "S_BAND"
	default:
		return "UNSPECIFIED"
	}
}

// Endpoint represents an OTAP network endpoint
type Endpoint struct {
	ID          string          `json:"id"`
	NodeID      string          `json:"node_id"`
	OperatorID  string          `json:"operator_id"`
	Address     string          `json:"address"`
	AWGPort     int32           `json:"awg_port"`
	Coordinates *GeoCoordinates `json:"coordinates,omitempty"`
	Status      EndpointStatus  `json:"status"`
	CreatedAt   time.Time       `json:"created_at"`
}

type EndpointStatus int32

const (
	EndpointStatusUnspecified EndpointStatus = 0
	EndpointStatusActive      EndpointStatus = 1
	EndpointStatusInactive    EndpointStatus = 2
	EndpointStatusSuspended   EndpointStatus = 3
)

func (s EndpointStatus) String() string {
	switch s {
	case EndpointStatusActive:
		return "ACTIVE"
	case EndpointStatusInactive:
		return "INACTIVE"
	case EndpointStatusSuspended:
		return "SUSPENDED"
	default:
		return "UNSPECIFIED"
	}
}

type GeoCoordinates struct {
	Lat  float64 `json:"lat"`
	Long float64 `json:"long"`
}

// Lease represents a wavelength lease allocation
type Lease struct {
	ID         string      `json:"id"`
	Wavelength *Wavelength `json:"wavelength"`
	EndpointID string      `json:"endpoint_id"`
	OperatorID string      `json:"operator_id"`
	Status     LeaseStatus `json:"status"`
	StartTime  time.Time   `json:"start_time"`
	EndTime    time.Time   `json:"end_time"`
	CreatedAt  time.Time   `json:"created_at"`
	UpdatedAt  time.Time   `json:"updated_at"`
	TokenHash  []byte      `json:"token_hash"`
	ParentHash []byte      `json:"parent_hash"`
}

type LeaseStatus int32

const (
	LeaseStatusUnspecified LeaseStatus = 0
	LeaseStatusActive      LeaseStatus = 1
	LeaseStatusExpired     LeaseStatus = 2
	LeaseStatusRevoked     LeaseStatus = 3
	LeaseStatusPending     LeaseStatus = 4
)

func (s LeaseStatus) String() string {
	switch s {
	case LeaseStatusActive:
		return "ACTIVE"
	case LeaseStatusExpired:
		return "EXPIRED"
	case LeaseStatusRevoked:
		return "REVOKED"
	case LeaseStatusPending:
		return "PENDING"
	default:
		return "UNSPECIFIED"
	}
}

// LeaseToken is the cryptographically signed token proving lease validity
type LeaseToken struct {
	Version    int32       `json:"version"`
	LeaseID    string      `json:"lease_id"`
	OperatorID string      `json:"operator_id"`
	Wavelength *Wavelength `json:"wavelength"`
	EndpointID string      `json:"endpoint_id"`
	StartTime  time.Time   `json:"start_time"`
	EndTime    time.Time   `json:"end_time"`
	Nonce      []byte      `json:"nonce"`
	Signature  []byte      `json:"signature"`
	IssuedAt   time.Time   `json:"issued_at"`
}

// MerkleNode represents a node in the Merkle tree
type MerkleNode struct {
	Hash    []byte      `json:"hash"`
	Left    *MerkleNode `json:"left,omitempty"`
	Right   *MerkleNode `json:"right,omitempty"`
	LeaseID string      `json:"lease_id,omitempty"`
	IsLeaf  bool        `json:"is_leaf"`
}

// MerkleCommitment is the signed root of a Merkle tree
type MerkleCommitment struct {
	OperatorID  string    `json:"operator_id"`
	RootHash    []byte    `json:"root_hash"`
	Timestamp   time.Time `json:"timestamp"`
	Signature   []byte    `json:"signature"`
	LeaseCount  int32     `json:"lease_count"`
	BlockHeight int64     `json:"block_height"`
}

// Operator represents a federated registry participant
type Operator struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	PublicKey []byte         `json:"public_key"`
	Endpoint  string         `json:"endpoint"`
	Status    OperatorStatus `json:"status"`
	JoinedAt  time.Time      `json:"joined_at"`
	LastSeen  time.Time      `json:"last_seen"`
}

type OperatorStatus int32

const (
	OperatorStatusUnspecified OperatorStatus = 0
	OperatorStatusActive      OperatorStatus = 1
	OperatorStatusInactive    OperatorStatus = 2
	OperatorStatusSuspended   OperatorStatus = 3
)

func (s OperatorStatus) String() string {
	switch s {
	case OperatorStatusActive:
		return "ACTIVE"
	case OperatorStatusInactive:
		return "INACTIVE"
	case OperatorStatusSuspended:
		return "SUSPENDED"
	default:
		return "UNSPECIFIED"
	}
}

// TranslationEntry maps wavelengths across operator boundaries
type TranslationEntry struct {
	ID             string            `json:"id"`
	FromOperator   string            `json:"from_operator"`
	ToOperator     string            `json:"to_operator"`
	FromWavelength *Wavelength       `json:"from_wavelength"`
	ToWavelength   *Wavelength       `json:"to_wavelength"`
	FromAWGPort    int32             `json:"from_awg_port"`
	ToAWGPort      int32             `json:"to_awg_port"`
	Status         TranslationStatus `json:"status"`
	EffectiveTime  time.Time         `json:"effective_time"`
	ExpiryTime     time.Time         `json:"expiry_time"`
}

type TranslationStatus int32

const (
	TranslationStatusUnspecified TranslationStatus = 0
	TranslationStatusActive      TranslationStatus = 1
	TranslationStatusPending     TranslationStatus = 2
	TranslationStatusExpired     TranslationStatus = 3
)

func (s TranslationStatus) String() string {
	switch s {
	case TranslationStatusActive:
		return "ACTIVE"
	case TranslationStatusPending:
		return "PENDING"
	case TranslationStatusExpired:
		return "EXPIRED"
	default:
		return "UNSPECIFIED"
	}
}

// ProofOfInvalidity demonstrates a registry violation
type ProofOfInvalidity struct {
	Type        InvalidityType    `json:"type"`
	LeaseA      *Lease            `json:"lease_a"`
	LeaseB      *Lease            `json:"lease_b,omitempty"`
	Commitment  *MerkleCommitment `json:"commitment"`
	MerkleProof [][]byte          `json:"merkle_proof"`
	Timestamp   time.Time         `json:"timestamp"`
}

type InvalidityType int32

const (
	InvalidityUnspecified       InvalidityType = 0
	InvalidityDoubleAllocation  InvalidityType = 1
	InvalidityExpiredLease      InvalidityType = 2
	InvalidityInvalidSignature  InvalidityType = 3
	InvalidityUnauthorizedOp    InvalidityType = 4
)

func (t InvalidityType) String() string {
	switch t {
	case InvalidityDoubleAllocation:
		return "DOUBLE_ALLOCATION"
	case InvalidityExpiredLease:
		return "EXPIRED_LEASE"
	case InvalidityInvalidSignature:
		return "INVALID_SIGNATURE"
	case InvalidityUnauthorizedOp:
		return "UNAUTHORIZED_OPERATION"
	default:
		return "UNSPECIFIED"
	}
}

// AuditLogEntry records all registry mutations
type AuditLogEntry struct {
	Timestamp  time.Time `json:"timestamp"`
	Operation  string    `json:"operation"`
	OperatorID string    `json:"operator_id"`
	LeaseID    string    `json:"lease_id"`
	Details    []byte    `json:"details"`
	Hash       []byte    `json:"hash"`
	PrevHash   []byte    `json:"prev_hash"`
}

// RegistryUpdate is a streaming update event
type RegistryUpdate struct {
	Operation   string            `json:"operation"`
	Lease       *Lease            `json:"lease,omitempty"`
	Commitment  *MerkleCommitment `json:"commitment,omitempty"`
	BlockHeight int64             `json:"block_height"`
	Timestamp   time.Time         `json:"timestamp"`
}
