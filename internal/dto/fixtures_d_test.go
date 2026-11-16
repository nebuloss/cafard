package dto

func init() {
	yes, no := true, false
	trackFixtures = append(trackFixtures,
		fixture{"chat_prefs", ChatPrefs{Pinned: &yes, Muted: &no}},
		fixture{"chat_read", ChatRead{UpToSeq: 48420}},
		fixture{"ack_request", AckRequest{Seqs: []int64{48395, 48410}}},
		fixture{"acked", Acked{Acked: 2}},
	)
}
