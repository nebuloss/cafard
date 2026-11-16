package dto

func init() {
	trackFixtures = append(trackFixtures,
		fixture{"token_create", TokenCreate{Principal: "claude-laptop", Kind: KindAgent, Label: "laptop MCP", Scopes: []Scope{ScopeRead, ScopeMessage}}},
		fixture{"pairing_create", PairingCreate{NewName: "guillaume-phone", Scopes: []Scope{ScopeRead, ScopeMessage, ScopeIngest}}},
		fixture{"mark_create", MarkCreate{Stream: "serial:dut1", Label: "test 42 starts"}},
		fixture{"mark_created", MarkCreated{Seq: 48230}},
	)
}
