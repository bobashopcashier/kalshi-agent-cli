package contract

const SchemaVersion = "kalshi.agent/v1"

type Effect struct {
	Class                string `json:"class"`
	Network              bool   `json:"network"`
	Mutation             bool   `json:"mutation"`
	PotentialMutation    bool   `json:"potential_mutation"`
	AuthRequired         bool   `json:"auth_required"`
	AuthOptional         bool   `json:"auth_optional,omitempty"`
	ConfirmationRequired bool   `json:"confirmation_required"`
	DryRun               bool   `json:"dry_run"`
	MutationStatus       string `json:"mutation_status"`
}

type Pagination struct {
	PagesFetched  int    `json:"pages_fetched"`
	ItemsScanned  int    `json:"items_scanned"`
	ItemsReturned int    `json:"items_returned"`
	NextCursor    string `json:"next_cursor,omitempty"`
}

type Truncation struct {
	Truncated bool     `json:"truncated"`
	Reasons   []string `json:"reasons"`
}

type Retry struct {
	Attempts         int    `json:"attempts"`
	Retries          int    `json:"retries"`
	Exhausted        bool   `json:"exhausted"`
	LastHTTPStatus   int    `json:"last_http_status,omitempty"`
	LastRetryAfterMS *int64 `json:"last_retry_after_ms,omitempty"`
}

type Meta struct {
	RequestID      string      `json:"request_id"`
	Environment    string      `json:"environment"`
	SanitizedCount int         `json:"sanitized_count"`
	Pagination     *Pagination `json:"pagination,omitempty"`
	Retry          *Retry      `json:"retry,omitempty"`
	Truncation     Truncation  `json:"truncation"`
	Sequence       *int        `json:"sequence,omitempty"`
}

type APIError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Details    any    `json:"details,omitempty"`
}

type Envelope struct {
	SchemaVersion         string    `json:"schema_version"`
	OutputContractVersion string    `json:"output_contract_version"`
	OK                    bool      `json:"ok"`
	Command               string    `json:"command"`
	RecordType            string    `json:"record_type,omitempty"`
	Effect                Effect    `json:"effect"`
	Data                  any       `json:"data,omitempty"`
	Error                 *APIError `json:"error,omitempty"`
	Meta                  Meta      `json:"meta"`
}

const (
	ExitOK       = 0
	ExitUsage    = 2
	ExitPolicy   = 3
	ExitAuth     = 4
	ExitNetwork  = 5
	ExitUpstream = 6
	ExitOutput   = 7
	ExitInternal = 10
)
