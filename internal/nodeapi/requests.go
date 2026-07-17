package nodeapi

type ResetRequest struct {
	Reset *bool `json:"reset"`
}

func (r *ResetRequest) Validate() []Issue {
	if r.Reset == nil {
		return []Issue{MissingIssue([]any{"reset"}, "boolean")}
	}
	return nil
}

type TagRequest struct {
	Tag *string `json:"tag"`
}

func (r *TagRequest) Validate() []Issue {
	if r.Tag == nil {
		return []Issue{MissingIssue([]any{"tag"}, "string")}
	}
	return nil
}

type TagResetRequest struct {
	Tag   *string `json:"tag"`
	Reset *bool   `json:"reset"`
}

func (r *TagResetRequest) Validate() []Issue {
	issues := make([]Issue, 0, 2)
	if r.Tag == nil {
		issues = appendValidationIssues(issues, MissingIssue([]any{"tag"}, "string"))
	}
	if r.Reset == nil {
		issues = appendValidationIssues(issues, MissingIssue([]any{"reset"}, "boolean"))
	}
	return issues
}

type UsernameRequest struct {
	Username *string `json:"username"`
}

func (r *UsernameRequest) Validate() []Issue {
	if r.Username == nil {
		return []Issue{MissingIssue([]any{"username"}, "string")}
	}
	return nil
}

type UserIDRequest struct {
	UserID *string `json:"userId"`
}

func (r *UserIDRequest) Validate() []Issue {
	if r.UserID == nil {
		return []Issue{MissingIssue([]any{"userId"}, "string")}
	}
	return nil
}
