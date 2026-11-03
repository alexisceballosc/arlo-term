package relay

const (
	packetSize = 188
	syncByte   = 0x47
	patPID     = 0
	maxGOP     = 16 << 20
)

type packet []byte

func (p packet) pid() int {
	return int(p[1]&0x1f)<<8 | int(p[2])
}

func (p packet) randomAccess() bool {
	control := (p[3] >> 4) & 0x3
	return (control == 2 || control == 3) && p[4] > 0 && p[5]&0x40 != 0
}

func (p packet) pmtPID() int {
	if p[1]&0x40 == 0 {
		return -1
	}
	payload := p[4:]
	if (p[3]>>4)&0x3 == 3 {
		payload = p[5+int(p[4]):]
	}
	if len(payload) < 1 || len(payload) < 1+int(payload[0])+12 {
		return -1
	}
	section := payload[1+int(payload[0]):]
	return int(section[10]&0x1f)<<8 | int(section[11])
}

type gopCache struct {
	pat, pmt []byte
	pmtPID   int
	data     []byte
	ready    bool
}

func newGOPCache() *gopCache {
	return &gopCache{pmtPID: -1}
}

func (c *gopCache) add(p packet) {
	switch pid := p.pid(); {
	case pid == patPID:
		c.pat = append(c.pat[:0], p...)
		if id := p.pmtPID(); id >= 0 {
			c.pmtPID = id
		}
		return
	case pid == c.pmtPID:
		c.pmt = append(c.pmt[:0], p...)
		return
	}
	if p.randomAccess() {
		c.data = append(c.data[:0], c.pat...)
		c.data = append(c.data, c.pmt...)
		c.ready = c.pat != nil && c.pmt != nil
	}
	if !c.ready {
		return
	}
	if len(c.data)+packetSize > maxGOP {
		c.data, c.ready = c.data[:0], false
		return
	}
	c.data = append(c.data, p...)
}

func (c *gopCache) snapshot() []byte {
	if !c.ready {
		return nil
	}
	return append([]byte(nil), c.data...)
}
