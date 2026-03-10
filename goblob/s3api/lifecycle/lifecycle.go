package lifecycle

import "encoding/xml"

// LifecycleConfiguration is the S3 lifecycle config root document.
type LifecycleConfiguration struct {
	XMLName xml.Name `xml:"LifecycleConfiguration" json:"-"`
	Rules   []Rule   `xml:"Rule" json:"rules"`
}

// Rule describes one lifecycle rule.
type Rule struct {
	ID         string      `xml:"ID,omitempty" json:"id,omitempty"`
	Filter     Filter      `xml:"Filter" json:"filter"`
	Expiration *Expiration `xml:"Expiration,omitempty" json:"expiration,omitempty"`
	Transition *Transition `xml:"Transition,omitempty" json:"transition,omitempty"`
	Status     string      `xml:"Status" json:"status"`
}

type Filter struct {
	Prefix string `xml:"Prefix,omitempty" json:"prefix,omitempty"`
	Tag    Tag    `xml:"Tag,omitempty" json:"tag,omitempty"`
}

type Tag struct {
	Key   string `xml:"Key" json:"key"`
	Value string `xml:"Value" json:"value"`
}

type Expiration struct {
	Days int    `xml:"Days,omitempty" json:"days,omitempty"`
	Date string `xml:"Date,omitempty" json:"date,omitempty"`
}

type Transition struct {
	Days         int    `xml:"Days,omitempty" json:"days,omitempty"`
	StorageClass string `xml:"StorageClass" json:"storage_class"`
}

// ObjectMeta contains fields lifecycle evaluation needs.
type ObjectMeta struct {
	Key          string
	LastModified int64 // unix seconds
	Tags         map[string]string
}
