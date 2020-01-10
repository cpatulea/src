package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"reflect"
	"strconv"
	"strings"
	"unsafe"

	"github.com/mdlayher/genetlink"
	"github.com/mdlayher/netlink"
	"github.com/prometheus/prometheus/tsdb"
)

type RateInfo struct{}
type BSSParam struct{}

type StationInfo struct {
	InactiveTime  uint32   `netlink:"1" type:"fixed"`   // STA_INFO_INACTIVE_TIME
	RxBytes       uint32   `netlink:"2" type:"fixed"`   // STA_INFO_RX_BYTES
	TxBytes       uint32   `netlink:"3" type:"fixed"`   // STA_INFO_TX_BYTES
	Llid          uint16   `netlink:"4" type:"fixed"`   // STA_INFO_LLID
	Plid          uint16   `netlink:"5" type:"fixed"`   // STA_INFO_PLID
	PLinkState    uint8    `netlink:"6" type:"fixed"`   // STA_INFO_PLINK_STATE
	Signal        uint8    `netlink:"7" type:"fixed"`   // STA_INFO_SIGNAL
	TxBitrate     RateInfo `netlink:"8" type:"nested"`  // STA_INFO_TX_BITRATE
	RxPackets     uint32   `netlink:"9" type:"fixed"`   // STA_INFO_RX_PACKETS
	TxPackets     uint32   `netlink:"10" type:"fixed"`  // STA_INFO_TX_PACKETS
	TxRetries     uint32   `netlink:"11" type:"fixed"`  // STA_INFO_TX_RETRIES
	TxFailed      uint32   `netlink:"12" type:"fixed"`  // STA_INFO_TX_FAILED
	SignalAvg     uint8    `netlink:"13" type:"fixed"`  // STA_INFO_SIGNAL_AVG
	RxBitrate     RateInfo `netlink:"14" type:"nested"` // STA_INFO_RX_BITRATE
	BssParam      BSSParam `netlink:"15" type:"nested"` // STA_INFO_BSS_PARAM
	ConnectedTime uint32   `netlink:"16" type:"fixed"`  // STA_INFO_CONNECTED_TIME
}

const nlmsgAlignTo = 4

func nlmsgAlign(len int) int {
	return ((len) + nlmsgAlignTo - 1) & ^(nlmsgAlignTo - 1)
}

var nlmsgHeaderLen = nlmsgAlign(int(unsafe.Sizeof(netlink.Header{})))

const nlaAlignTo = 4

func nlaAlign(len int) int {
	return ((len) + nlaAlignTo - 1) & ^(nlaAlignTo - 1)
}

const sizeofAttribute = 4

var nlaHeaderLen = nlaAlign(sizeofAttribute)

func parseMessage(b []byte) (netlink.Message, int, error) {
	h := &netlink.Header{
		Length:   binary.BigEndian.Uint32(b[0:4]),
		Type:     netlink.HeaderType(binary.BigEndian.Uint16(b[4:6])),
		Flags:    netlink.HeaderFlags(binary.BigEndian.Uint16(b[6:8])),
		Sequence: binary.BigEndian.Uint32(b[8:12]),
		PID:      binary.BigEndian.Uint32(b[12:16]),
	}
	l := nlmsgAlign(int(h.Length))
	if int(h.Length) < nlmsgHeaderLen || l > len(b) {
		return netlink.Message{}, 0, fmt.Errorf("Short message")
	}
	m := netlink.Message{Header: *h, Data: b[nlmsgHeaderLen:int(h.Length)]}
	return m, l, nil
}

type attributeDecoder struct {
	b   []byte
	i   int
	ab  []byte
	err error
}

func newAttributeDecoder(b []byte) (*attributeDecoder, error) {
	ad := &attributeDecoder{
		b: b,
	}
	return ad, nil
}

func (ad *attributeDecoder) Next() bool {
	if ad.err != nil {
		return false
	}

	if ad.i >= len(ad.b) {
		return false
	}

	ad.ab = ad.b[ad.i:]
	if len(ad.ab) < nlaHeaderLen {
		ad.err = fmt.Errorf("Not enough bytes for attribute header (%d < %d)", len(ad.ab), nlaHeaderLen)
		return false
	}

	if ad.Len() > len(ad.ab) {
		ad.err = fmt.Errorf("Not enough bytes for attribute data (%d > %d)", ad.Len(), len(ad.ab))
		return false
	}

	if int(ad.Len()) < nlaHeaderLen {
		ad.i += nlaHeaderLen
	} else {
		ad.i += nlaAlign(int(ad.Len()))
	}

	return true
}

func (ad *attributeDecoder) Len() int {
	return int(binary.BigEndian.Uint16(ad.ab[0:2]))
}

func (ad *attributeDecoder) Type() uint16 {
	return binary.BigEndian.Uint16(ad.ab[2:4])
}

func (ad *attributeDecoder) Bytes() []byte {
	return ad.ab[nlaHeaderLen:ad.Len()]
}

func (ad *attributeDecoder) Err() error {
	return ad.err
}

func parseNetlink(path string, ts int64, m []string, data []byte, a tsdb.Appender) error {
	netdev := m[2]

	for len(data) >= nlmsgHeaderLen {
		msg, dlen, err := parseMessage(data)
		if err != nil {
			return fmt.Errorf("Parse netlink: %w", err)
		}
		data = data[dlen:]

		nlmsgMinType := 0x10
		if int(msg.Header.Type) < nlmsgMinType {
			log.Fatalf("nlmsg type too small: %d", msg.Header.Type)
		}
		if msg.Header.Flags != netlink.Multi {
			log.Fatalf("nlmsg flags unexpected: %s (0x%x)", msg.Header.Flags, msg.Header.Flags)
		}

		var gmsg genetlink.Message
		if err := gmsg.UnmarshalBinary(msg.Data); err != nil {
			log.Fatalf("genetlink: %s", err)
		}
		CmdNewStation := uint8(19)
		if gmsg.Header.Command != CmdNewStation {
			log.Fatalf("genetlink unexpected command %d", gmsg.Header.Command)
		}

		ad, err := newAttributeDecoder(gmsg.Data)
		if err != nil {
			log.Fatalf("nlmsg parse attribute: %s", err)
		}
		var station string
		for ad.Next() {
			AttrMac := uint16(6)
			AttrStaInfo := uint16(21)
			if ad.Type() == AttrMac {
				mac := net.HardwareAddr(ad.Bytes())
				if name, ok := stations[mac.String()]; ok {
					station = name
				} else {
					station = mac.String()
				}
			} else if ad.Type() == AttrStaInfo {
				if station == "" {
					log.Fatalf("no mac")
				}
				parseStaInfo(ts, netdev, station, ad.Bytes(), a)
			}
		}
		if ad.Err() != nil {
			log.Fatalf("attribute: %s", ad.Err())
		}
	}
	return nil
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		log.Fatal(err)
	}
	return n
}

func parseStaInfo(ts int64, netdev string, station string, data []byte, a tsdb.Appender) {
	ad, err := newAttributeDecoder(data)
	if err != nil {
		log.Fatalf("nlmsg stainfo: %s", err)
	}
	for ad.Next() {
		t := reflect.TypeOf(StationInfo{})
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if atoi(f.Tag.Get("netlink")) == int(ad.Type()) &&
				f.Tag.Get("type") == "fixed" {
				name := "stainfo_" + strings.ToLower(f.Name)
				value := reflect.Indirect(reflect.ValueOf(StationInfo{})).Field(i)
				err := binary.Read(bytes.NewBuffer(ad.Bytes()), binary.BigEndian, value.Interface())
				if err != nil {
					log.Fatal(err)
				}
				var floatValue float64
				switch value.Kind() {
				case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
					floatValue = float64(value.Int())
				case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
					floatValue = float64(value.Uint())
				default:
					log.Fatalf("unhandled kind %s", value.Kind())
				}
				addPoint(map[string]string{
					"job":      "nl80211",
					"netdev":   netdev,
					"var":      name,
					"__name__": name,
					"station":  station,
				}, ts, floatValue, a)
			}
		}
	}
	if ad.Err() != nil {
		log.Fatalf("sta attribute: %s", ad.Err())
	}
}
