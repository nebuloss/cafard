package rfc2217

// Telnet bytes (RFC 854) and the options cafard negotiates.
const (
	IAC  = 255
	DONT = 254
	DO   = 253
	WONT = 252
	WILL = 251
	SB   = 250
	GA   = 249
	NOP  = 241
	SE   = 240

	OptBinary  = 0
	OptSGA     = 3
	OptComPort = 44
)

// maxSub bounds a subnegotiation body; COM-PORT-OPTION ones are a few
// bytes, and a hostile peer must not grow memory without end.
const maxSub = 256

type parseState uint8

const (
	stData parseState = iota
	stIAC
	stVerb
	stSB
	stSBIAC
)

// parser splits a Telnet byte stream into data and commands. It keeps its
// state between calls, so a command may straddle two reads.
type parser struct {
	state  parseState
	verb   byte
	sub    []byte
	lastCR bool
	// binary is whether the peer sends in BINARY mode. Without it, NVT
	// rules apply and the NUL after a CR is padding.
	binary bool

	onVerb func(verb, opt byte)
	onSub  func(body []byte)
}

// feed parses in and appends the data bytes to out.
func (p *parser) feed(in []byte, out []byte) []byte {
	for i := 0; i < len(in); i++ {
		b := in[i]
		switch p.state {
		case stData:
			if b == IAC {
				p.state = stIAC
				continue
			}
			if b == 0 && p.lastCR && !p.binary {
				p.lastCR = false
				continue
			}
			p.lastCR = b == '\r'
			out = append(out, b)
		case stIAC:
			p.state = stData
			switch b {
			case IAC:
				p.lastCR = false
				out = append(out, IAC)
			case WILL, WONT, DO, DONT:
				p.verb = b
				p.state = stVerb
			case SB:
				p.sub = p.sub[:0]
				p.state = stSB
			}
			// Any other command (NOP, GA, a stray SE...) carries no data.
		case stVerb:
			p.state = stData
			if p.onVerb != nil {
				p.onVerb(p.verb, b)
			}
		case stSB:
			if b == IAC {
				p.state = stSBIAC
				continue
			}
			p.addSub(b)
		case stSBIAC:
			switch b {
			case IAC:
				p.addSub(IAC)
				p.state = stSB
			case SE:
				p.state = stData
				if p.onSub != nil {
					p.onSub(p.sub)
				}
			default:
				// Malformed: a command inside a subnegotiation. Drop the
				// subnegotiation and read b as a command.
				p.state = stIAC
				i--
			}
		}
	}
	return out
}

func (p *parser) addSub(b byte) {
	if len(p.sub) < maxSub {
		p.sub = append(p.sub, b)
	}
}

// escape doubles every IAC so data cannot be read as a command. Without
// BINARY a lone CR is sent as CR NUL, as NVT requires.
func escape(p []byte, binary bool) []byte {
	out := make([]byte, 0, len(p)+8)
	for i, b := range p {
		out = append(out, b)
		switch {
		case b == IAC:
			out = append(out, IAC)
		case b == '\r' && !binary && (i+1 == len(p) || p[i+1] != '\n'):
			out = append(out, 0)
		}
	}
	return out
}
