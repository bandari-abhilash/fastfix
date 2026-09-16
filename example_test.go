package fastfix_test

import (
	"fmt"
	"strings"

	"github.com/bandari-abhilash/fastfix"
)

const templatesXML = `<?xml version="1.0" encoding="UTF-8"?>
<templates xmlns="http://www.fixprotocol.org/ns/fast/td/1.1">
  <template name="Header">
    <string name="MessageType"/>
    <string name="SendingTime"><tail/></string>
  </template>
  <template name="Logon" id="1">
    <templateRef name="Header"/>
    <uInt32 name="HeartBtInt"/>
    <string name="Username"/>
    <string name="AppID" presence="optional"/>
  </template>
</templates>`

// Encode a session message and decode it back.
func Example() {
	reg, err := fastfix.LoadTemplates(strings.NewReader(templatesXML))
	if err != nil {
		panic(err)
	}

	packet, err := fastfix.Encode(reg.Get(1), map[string]any{
		"MessageType": "A",
		"SendingTime": "20260716-09:15:00.000000",
		"HeartBtInt":  30,
		"Username":    "USER01",
	})
	if err != nil {
		panic(err)
	}
	fmt.Printf("%d bytes on the wire\n", len(packet))

	dec := fastfix.NewDecoder(reg)
	msgs, err := dec.DecodePacket(packet)
	if err != nil {
		panic(err)
	}
	m := msgs[0]
	hb, _ := m.Int("HeartBtInt")
	fmt.Printf("%s user=%s heartbeat=%ds appID-present=%v\n",
		m.Template.Name, m.ValueString("Username"), hb, m.Has("AppID"))

	// Output:
	// 35 bytes on the wire
	// Logon user=USER01 heartbeat=30s appID-present=false
}
