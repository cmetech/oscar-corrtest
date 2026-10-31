// Package snmpload describes bounded native SNMP traffic; it does not claim UDP delivery.
package snmpload

import (
	"fmt"
	"regexp"
)

type Options struct {
	Port         int    `json:"port"`
	Profile      string `json:"profile"`
	Rate         int    `json:"packetsPerSecond"`
	Duration     int    `json:"offerSeconds"`
	ClearDelay   int    `json:"clearDelaySeconds"`
	DrainTimeout int    `json:"drainTimeoutSeconds"`
}

type Varbind struct {
	OID   string `json:"oid"`
	Type  string `json:"type"`
	Value string `json:"value"`
}
type Episode struct {
	Family        string    `json:"family"`
	Token         string    `json:"token"`
	Mapping       string    `json:"mapping"`
	MappingSource string    `json:"mappingSource"`
	MappingSHA256 string    `json:"mappingSha256"`
	AlertName     string    `json:"alertName"`
	AlertGroup    string    `json:"alertGroup"`
	Agent         string    `json:"agent"`
	Severity      string    `json:"severity"`
	AlertKey      string    `json:"alertKey"`
	ProblemOID    string    `json:"problemOid"`
	ClearOID      string    `json:"clearOid"`
	Varbinds      []Varbind `json:"varbinds"`
}
type Packet struct {
	Episode  int    `json:"episode"`
	Kind     string `json:"kind"`
	AtMillis int64  `json:"atMillis"`
}
type Plan struct {
	RunID        string    `json:"runId"`
	Pattern      string    `json:"pattern"`
	InputPath    string    `json:"inputPath"`
	Options      Options   `json:"options"`
	OfferPackets int       `json:"offerPackets"`
	Episodes     []Episode `json:"episodes"`
	Packets      []Packet  `json:"packets"`
}

var Families = []string{"dhcpv6-limit", "dhcp-limit", "aaa-server", "address-pool"}

func (o Options) Validate() error {
	if o.Port < 1 || o.Port > 65535 || o.Rate < 1 || o.Rate > 25 || o.Duration < 10 || o.Duration > 600 || o.ClearDelay < 1 || o.ClearDelay > 120 || o.DrainTimeout < 10 || o.DrainTimeout > 1800 {
		return fmt.Errorf("SNMP load requires port 1–65535, rate 1–25 packets/s, duration 10–600s, clear delay 1–120s and drain timeout 10–1800s")
	}
	if o.Profile != "mixed" {
		for _, f := range Families {
			if o.Profile == f {
				return nil
			}
		}
		return fmt.Errorf("unknown SNMP load profile")
	}
	return nil
}

// Fixture derives identities from the run, while retaining the actual migrated OIDs and MIB types.
func Fixture(runID string, sequence int, family string) (Episode, error) {
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`).MatchString(runID) || sequence < 0 || sequence > 15000 {
		return Episode{}, fmt.Errorf("invalid SNMP fixture identity")
	}
	token := fmt.Sprintf("ct-%s-%d", runID, sequence)
	e := Episode{Family: family, Token: token, Severity: "major"}
	switch family {
	case "dhcpv6-limit":
		e.Mapping = "1.3.6.1.4.1.2636.3.62.62.2.3"
		e.MappingSource = "juniper-jdhcpv6-mapping.json"
		e.MappingSHA256 = "a108b4efdc582f2fbcc80de40277c48320c6c3728e5d09b82fa0d5097eba534a"
		e.AlertName = "jnx_jdhcpv6_local_server_interface_limit_status"
		e.AlertGroup = "jnxJdhcpv6LocalServerInterfaceLimitStatus"
		e.Agent = "JUNIPER-JDHCPV6-MIB"
		e.ProblemOID = e.Mapping + ".0.1"
		e.ClearOID = e.Mapping + ".0.2"
		iface := fmt.Sprintf("ge-0/0/%d.200", sequence%48)
		e.AlertKey = "jnxJdhcpv6RouterName:" + token + ", jnxJdhcpv6LocalServerInterfaceName:" + iface
		e.Varbinds = []Varbind{{"1.3.6.1.4.1.2636.3.62.62.2.4.1.0", "s", token}, {"1.3.6.1.4.1.2636.3.62.62.2.4.2.0", "s", iface}, {"1.3.6.1.4.1.2636.3.62.62.2.4.3.0", "u", "2048"}}
	case "dhcp-limit":
		e.Mapping = "1.3.6.1.4.1.2636.3.61.61.1.3"
		e.MappingSource = "juniper-jdhcp-jdhcp-1-3-mapping.json"
		e.MappingSHA256 = "97816fe5ee90dc61434f64b042604d6bbd3622a08d6ab33402703681c34cd462"
		e.AlertName = "jnx_jdhcp_local_server_interface_limit_status"
		e.AlertGroup = "jnxJdhcpLocalServerInterfaceLimitStatus"
		e.Agent = "JUNIPER-JDHCP-MIB"
		e.ProblemOID = e.Mapping + ".0.2"
		e.ClearOID = e.Mapping + ".0.3"
		iface := fmt.Sprintf("ge-0/0/%d.100", sequence%48)
		e.AlertKey = "jnxJdhcpRouterName:" + token + ", jnxJdhcpLocalServerInterfaceName:" + iface
		e.Varbinds = []Varbind{{"1.3.6.1.4.1.2636.3.61.61.1.4.2.0", "s", token}, {"1.3.6.1.4.1.2636.3.61.61.1.4.4.0", "s", iface}, {"1.3.6.1.4.1.2636.3.61.61.1.4.5.0", "u", "1024"}}
	case "aaa-server", "address-pool":
		e.Mapping = "1.3.6.1.4.1.2636.3.51.1"
		e.MappingSource = "juniper-user-aaa-mapping.json"
		e.MappingSHA256 = "d830dd889e87773fad3ad87a54c8eac6869cf7dac2ed4fb95798e521a812dad9"
		e.Agent = "JUNIPER-USER-AAA-MIB"
		if family == "aaa-server" {
			e.AlertName = "jnx_access_auth_server_status"
			e.AlertGroup = "jnxAccessAuthServerStatus"
			e.Severity = "critical"
			e.AlertKey = "jnxUserAAAServerName:" + token
			e.ProblemOID = e.Mapping + ".0.3"
			e.ClearOID = e.Mapping + ".0.4"
			e.Varbinds = []Varbind{{e.Mapping + ".1.3.1.0", "s", token}}
		} else {
			e.AlertName = "jnx_access_auth_address_pool_threshold_status"
			e.AlertGroup = "jnxAccessAuthAddressPoolThresholdStatus"
			e.AlertKey = "jnxUserAAAAddressPoolName:" + token
			e.ProblemOID = e.Mapping + ".0.5"
			e.ClearOID = e.Mapping + ".0.6"
			e.Varbinds = []Varbind{{e.Mapping + ".1.3.2.0", "s", token}}
		}
	default:
		return Episode{}, fmt.Errorf("unknown SNMP fixture family")
	}
	return e, nil
}

func Compile(runID string, o Options) (Plan, error) {
	if err := o.Validate(); err != nil {
		return Plan{}, err
	}
	p := Plan{RunID: runID, Pattern: "snmp-load", InputPath: "snmp-load", Options: o, OfferPackets: o.Rate * o.Duration}
	created := []int64{}
	repeated := []bool{}
	pending := []int{}
	// Each slot is one packet; clears and repeats consume the same rate budget.
	slots := "PPPPRCPCCC"
	for i := 0; i < p.OfferPackets; i++ {
		at := int64(i) * 1000 / int64(o.Rate)
		kind := "problem"
		id := -1
		if slots[i%len(slots)] == 'C' && len(pending) > 0 && at-created[pending[0]] >= int64(o.ClearDelay)*1000 {
			kind = "clear"
			id = pending[0]
			pending = pending[1:]
		} else if slots[i%len(slots)] == 'R' {
			for _, n := range pending {
				if !repeated[n] && at-created[n] >= 1000 && created[n]+int64(o.ClearDelay)*1000-at >= 1000 {
					id = n
					kind = "repeat"
					repeated[n] = true
					break
				}
			}
		}
		if id < 0 {
			id = len(p.Episodes)
			family := o.Profile
			if family == "mixed" {
				family = Families[[]int{0, 1, 2, 3, 0, 1, 0, 2, 1, 0}[id%10]]
			}
			e, err := Fixture(runID, id, family)
			if err != nil {
				return Plan{}, err
			}
			p.Episodes = append(p.Episodes, e)
			created = append(created, at)
			repeated = append(repeated, false)
			pending = append(pending, id)
		}
		p.Packets = append(p.Packets, Packet{Episode: id, Kind: kind, AtMillis: at})
	}
	at := int64(o.Duration) * 1000
	for _, id := range pending {
		if due := created[id] + int64(o.ClearDelay)*1000; due > at {
			at = due
		}
		p.Packets = append(p.Packets, Packet{Episode: id, Kind: "clear", AtMillis: at})
		at += (1000 + int64(o.Rate) - 1) / int64(o.Rate)
	}
	return p, nil
}
