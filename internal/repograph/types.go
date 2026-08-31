package repograph

import "time"

const SchemaVersion = 2

type NodeKind string

const (
	NodeFile    NodeKind = "file"
	NodeSymbol  NodeKind = "symbol"
	NodeRoute   NodeKind = "route"
	NodeLiteral NodeKind = "literal"
)

type EdgeKind string

const (
	EdgeContains  EdgeKind = "contains"
	EdgeImports   EdgeKind = "imports"
	EdgeCalls     EdgeKind = "calls"
	EdgeTests     EdgeKind = "tests"
	EdgeRoutes    EdgeKind = "routes"
	EdgeShares    EdgeKind = "shares_literal"
	EdgeInherits  EdgeKind = "inherits"
	EdgeCoChanges EdgeKind = "cochanges"
)

type SymbolFact struct {
	Name      string `json:"name"`
	Qualified string `json:"qualified,omitempty"`
	Kind      string `json:"kind"`
	Signature string `json:"signature,omitempty"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Parent    string `json:"parent,omitempty"`
	Exported  bool   `json:"exported,omitempty"`
}

type ImportFact struct {
	Target string `json:"target"`
	Line   int    `json:"line"`
}

type CallFact struct {
	Caller string `json:"caller,omitempty"`
	Callee string `json:"callee"`
	Line   int    `json:"line"`
}

type LiteralFact struct {
	Value string `json:"value"`
	Kind  string `json:"kind"`
	Line  int    `json:"line"`
}

type RouteFact struct {
	Method string `json:"method,omitempty"`
	Path   string `json:"path"`
	Owner  string `json:"owner,omitempty"`
	Line   int    `json:"line"`
}

type InheritanceFact struct {
	Child  string `json:"child"`
	Parent string `json:"parent"`
	Line   int    `json:"line"`
}

type FileFacts struct {
	Path        string            `json:"path"`
	Language    string            `json:"language"`
	Hash        string            `json:"hash"`
	Size        int64             `json:"size"`
	Generated   bool              `json:"generated,omitempty"`
	Symbols     []SymbolFact      `json:"symbols,omitempty"`
	Imports     []ImportFact      `json:"imports,omitempty"`
	Calls       []CallFact        `json:"calls,omitempty"`
	Literals    []LiteralFact     `json:"literals,omitempty"`
	Routes      []RouteFact       `json:"routes,omitempty"`
	Inheritance []InheritanceFact `json:"inheritance,omitempty"`
	Warnings    []string          `json:"warnings,omitempty"`
}

type Node struct {
	ID        string   `json:"id"`
	Kind      NodeKind `json:"kind"`
	Name      string   `json:"name"`
	Qualified string   `json:"qualified,omitempty"`
	Path      string   `json:"path,omitempty"`
	Language  string   `json:"language,omitempty"`
	Symbol    string   `json:"symbol_kind,omitempty"`
	Signature string   `json:"signature,omitempty"`
	Line      int      `json:"line,omitempty"`
	EndLine   int      `json:"end_line,omitempty"`
	Test      bool     `json:"test,omitempty"`
}

type Edge struct {
	From     string   `json:"from"`
	To       string   `json:"to"`
	Kind     EdgeKind `json:"kind"`
	Weight   int      `json:"weight"`
	Path     string   `json:"path,omitempty"`
	Line     int      `json:"line,omitempty"`
	Evidence string   `json:"evidence,omitempty"`
}

type Coverage struct {
	Discovered  int      `json:"discovered"`
	Indexed     int      `json:"indexed"`
	Reused      int      `json:"reused"`
	Unsupported int      `json:"unsupported"`
	Generated   int      `json:"generated"`
	Oversized   int      `json:"oversized"`
	Unreadable  int      `json:"unreadable"`
	Omitted     int      `json:"omitted"`
	Warnings    []string `json:"warnings,omitempty"`
}

type Snapshot struct {
	Schema     int                  `json:"schema"`
	Root       string               `json:"root"`
	Generation uint64               `json:"generation"`
	BuiltAt    time.Time            `json:"built_at"`
	Facts      map[string]FileFacts `json:"facts"`
	Nodes      []Node               `json:"nodes"`
	Edges      []Edge               `json:"edges"`
	Coverage   Coverage             `json:"coverage"`
	index      *graphIndex
}

type Scope struct {
	Path     string `json:"path,omitempty"`
	Language string `json:"language,omitempty"`
	Kind     string `json:"kind,omitempty"`
}

type ReadWindow struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Offset    int    `json:"offset"`
	Limit     int    `json:"limit"`
}

type Hit struct {
	NodeID    string     `json:"node_id"`
	Name      string     `json:"name"`
	Kind      NodeKind   `json:"kind"`
	Path      string     `json:"path,omitempty"`
	Line      int        `json:"line,omitempty"`
	EndLine   int        `json:"end_line,omitempty"`
	Language  string     `json:"language,omitempty"`
	Relation  EdgeKind   `json:"relation,omitempty"`
	Direction string     `json:"direction,omitempty"`
	Via       []EdgeKind `json:"via,omitempty"`
	Score     int64      `json:"score"`
}

type ResultMeta struct {
	Operation  string   `json:"operation"`
	Root       string   `json:"root"`
	Scope      Scope    `json:"scope,omitempty"`
	Status     string   `json:"status"`
	Generation uint64   `json:"generation"`
	Depth      int      `json:"depth,omitempty"`
	Truncated  bool     `json:"truncated"`
	Degraded   bool     `json:"degraded"`
	UsedTokens int      `json:"used_tokens"`
	MaxTokens  int      `json:"max_tokens"`
	Warnings   []string `json:"warnings,omitempty"`
	Coverage   Coverage `json:"coverage"`
}

type Result struct {
	Text           string       `json:"text"`
	Hits           []Hit        `json:"hits,omitempty"`
	SuggestedReads []ReadWindow `json:"suggested_reads,omitempty"`
	Meta           ResultMeta   `json:"meta"`
}

type FocusOptions struct {
	SessionID string
	Query     string
	Scope     Scope
	Fresh     bool
	MaxTokens int
}

type ImpactOptions struct {
	Files       []string
	Symbols     []string
	Uncommitted bool
	Base        string
	MaxTokens   int
	warnings    []string
	cochanges   map[string]int64
}

type BuildReport struct {
	Parsed []string
	Reused []string
}
