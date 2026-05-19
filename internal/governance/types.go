package governance

const (
	ModerationNone     = "none"
	ModerationFrozen   = "frozen"
	ModerationBlocked  = "blocked"
	ModerationLegalHold = "legal_hold"
	ModerationAppealed  = "appealed"

	AccessPublic    = "public"
	AccessPrivate   = "private"
	AccessSuspended = "suspended"
	AccessBlocked   = "blocked"

	StoragePending     = "pending"
	StorageActive      = "active"
	StorageExpired     = "expired"
	StorageTerminating = "terminating"
	StorageDeleted     = "deleted"

	DeletionStandard  = "standard"
	DeletionRetain    = "retain_evidence"
	DeletionImmediate = "immediate"
)

type Action string

const (
	ActionFreeze     Action = "freeze"
	ActionBlock      Action = "block"
	ActionLegalHold  Action = "legal_hold"
	ActionAppeal     Action = "appeal"
)

type AuditRecord struct {
	IntentID    string `json:"intent_id"`
	Action      Action `json:"action"`
	Operator    string `json:"operator,omitempty"`
	Reason      string `json:"reason,omitempty"`
	ExpiresAt   int64  `json:"expires_at_unix,omitempty"`
	PerformedAt int64  `json:"performed_at_unix"`
}

func ValidAction(a string) bool {
	switch Action(a) {
	case ActionFreeze, ActionBlock, ActionLegalHold, ActionAppeal:
		return true
	}
	return false
}
