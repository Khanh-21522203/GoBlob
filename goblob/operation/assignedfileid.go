package operation

import (
	"encoding/json"
	"fmt"
)

// AssignedFileId is the result of a /dir/assign call.
type AssignedFileId struct {
	Fid       string `json:"fid"`
	Url       string `json:"url"`
	PublicUrl string `json:"publicUrl"`
	Count     uint64 `json:"count"`
	Auth      string `json:"auth"`
	Error     string `json:"error,omitempty"`
}

// UnmarshalJSON supports both publicUrl and public_url response fields.
func (a *AssignedFileId) UnmarshalJSON(data []byte) error {
	type alias AssignedFileId
	var aux struct {
		alias
		PublicURLAlt string `json:"public_url"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*a = AssignedFileId(aux.alias)
	if a.PublicUrl == "" && aux.PublicURLAlt != "" {
		a.PublicUrl = aux.PublicURLAlt
	}
	if a.Fid == "" && a.Error == "" {
		return fmt.Errorf("missing fid in assign response")
	}
	return nil
}
