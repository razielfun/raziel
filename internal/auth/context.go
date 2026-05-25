package auth

type Scope string

const (
	ScopeRead   Scope = "read"
	ScopeDeploy Scope = "deploy"
	ScopeDelete Scope = "delete"
	ScopeAdmin  Scope = "admin"
)

// impliedBy maps a scope to all scopes it is implied by.
var impliedBy = map[Scope][]Scope{
	ScopeRead:   {ScopeRead, ScopeDeploy, ScopeDelete, ScopeAdmin},
	ScopeDeploy: {ScopeDeploy, ScopeAdmin},
	ScopeDelete: {ScopeDelete, ScopeAdmin},
	ScopeAdmin:  {ScopeAdmin},
}

type Context struct {
	Token       string
	TenantID    string
	PrincipalID string
	APIKeyID    string
	Scopes      []Scope
}

func (c Context) HasScope(required Scope) bool {
	granted := impliedBy[required]
	for _, have := range c.Scopes {
		for _, g := range granted {
			if have == g {
				return true
			}
		}
	}
	return false
}

func AllScopes() []Scope {
	return []Scope{ScopeRead, ScopeDeploy, ScopeDelete, ScopeAdmin}
}
