package project

import "time"

type Snapshot struct {
	Project   ProjectInfo `json:"project"`
	Summary   Summary     `json:"summary"`
	Modules   []Module    `json:"modules"`
	Changes   []Change    `json:"changes"`
	Languages []Language  `json:"languages"`
	ScannedAt time.Time   `json:"scannedAt"`
}

type ProjectInfo struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Branch string `json:"branch"`
	IsGit  bool   `json:"isGit"`
}

type Summary struct {
	Files       int   `json:"files"`
	CodeLines   int   `json:"codeLines"`
	Modules     int   `json:"modules"`
	Changes     int   `json:"changes"`
	Insertions  int   `json:"insertions"`
	Deletions   int   `json:"deletions"`
	LastScanMS  int64 `json:"lastScanMs"`
	SourceFiles int   `json:"sourceFiles"`
}

type Module struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Description string `json:"description"`
	Files       int    `json:"files"`
	CodeLines   int    `json:"codeLines"`
	Status      string `json:"status"`
}

type Change struct {
	Path       string `json:"path"`
	Status     string `json:"status"`
	Insertions int    `json:"insertions"`
	Deletions  int    `json:"deletions"`
	Staged     bool   `json:"staged"`
	Unstaged   bool   `json:"unstaged"`
}

type Diff struct {
	Path      string `json:"path"`
	Mode      string `json:"mode"`
	Content   string `json:"content"`
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
}

type Language struct {
	Name       string  `json:"name"`
	Files      int     `json:"files"`
	Lines      int     `json:"lines"`
	Percentage float64 `json:"percentage"`
	Color      string  `json:"color"`
}
