package amqp

import (
	"encoding/binary"
	"fmt"
)

type ConnectionClose struct {
	ReplyCode uint16
	ReplyText string
	ClassID   uint16
	MethodID  uint16
}

type ChannelClose struct {
	ReplyCode uint16
	ReplyText string
	ClassID   uint16
	MethodID  uint16
}

type ConnectionBlocked struct {
	Reason string
}

type ChannelFlow struct {
	Active bool
}

type ConnectionTune struct {
	ChannelMax uint16
	FrameMax   uint32
	Heartbeat  uint16
}

type BasicReturn struct {
	ReplyCode  uint16
	ReplyText  string
	Exchange   string
	RoutingKey string
}

type BasicPublish struct {
	Exchange   string
	RoutingKey string
	Mandatory  bool
	Immediate  bool
}

type BasicConsume struct {
	Queue       string
	ConsumerTag string
	NoAck       bool
	Exclusive   bool
}

type BasicCancel struct {
	ConsumerTag string
}

type BasicNack struct {
	DeliveryTag uint64
	Multiple    bool
	Requeue     bool
}

type BasicReject struct {
	DeliveryTag uint64
	Requeue     bool
}

type QueueDeclare struct {
	Queue      string
	Passive    bool
	Durable    bool
	Exclusive  bool
	AutoDelete bool
}

type QueueDelete struct {
	Queue    string
	IfUnused bool
	IfEmpty  bool
}

type QueuePurge struct {
	Queue string
}

type ExchangeDeclare struct {
	Exchange   string
	Type       string
	Passive    bool
	Durable    bool
	AutoDelete bool
}

type ExchangeDelete struct {
	Exchange string
	IfUnused bool
}

// ── helpers ──────────────────────────────────────────────────────────────────

func parseShortStr(data []byte, offset int) (string, int, error) {
	if len(data) <= offset {
		return "", offset, fmt.Errorf("out of bounds at %d", offset)
	}
	length := int(data[offset])
	offset++
	if len(data) < offset+length {
		return "", offset, fmt.Errorf("short string truncated (need %d got %d)", length, len(data)-offset)
	}
	return string(data[offset : offset+length]), offset + length, nil
}

func parseShortUint(data []byte, offset int) (uint16, int, error) {
	if len(data) < offset+2 {
		return 0, offset, fmt.Errorf("short uint truncated")
	}
	return binary.BigEndian.Uint16(data[offset : offset+2]), offset + 2, nil
}

func parseLongUint(data []byte, offset int) (uint32, int, error) {
	if len(data) < offset+4 {
		return 0, offset, fmt.Errorf("long uint truncated")
	}
	return binary.BigEndian.Uint32(data[offset : offset+4]), offset + 4, nil
}

func parseLongLongUint(data []byte, offset int) (uint64, int, error) {
	if len(data) < offset+8 {
		return 0, offset, fmt.Errorf("longlong uint truncated")
	}
	return binary.BigEndian.Uint64(data[offset : offset+8]), offset + 8, nil
}

// ── parsers ───────────────────────────────────────────────────────────────────

func ParseConnectionClose(payload []byte) (*ConnectionClose, error) {
	if len(payload) < 2 {
		return nil, fmt.Errorf("too short")
	}
	cc := &ConnectionClose{ReplyCode: binary.BigEndian.Uint16(payload[0:2])}
	text, offset, err := parseShortStr(payload, 2)
	if err != nil {
		return cc, err
	}
	cc.ReplyText = text
	if len(payload) >= offset+4 {
		cc.ClassID = binary.BigEndian.Uint16(payload[offset : offset+2])
		cc.MethodID = binary.BigEndian.Uint16(payload[offset+2 : offset+4])
	}
	return cc, nil
}

func ParseChannelClose(payload []byte) (*ChannelClose, error) {
	if len(payload) < 2 {
		return nil, fmt.Errorf("too short")
	}
	cc := &ChannelClose{ReplyCode: binary.BigEndian.Uint16(payload[0:2])}
	text, offset, err := parseShortStr(payload, 2)
	if err != nil {
		return cc, err
	}
	cc.ReplyText = text
	if len(payload) >= offset+4 {
		cc.ClassID = binary.BigEndian.Uint16(payload[offset : offset+2])
		cc.MethodID = binary.BigEndian.Uint16(payload[offset+2 : offset+4])
	}
	return cc, nil
}

func ParseConnectionBlocked(payload []byte) (*ConnectionBlocked, error) {
	text, _, err := parseShortStr(payload, 0)
	if err != nil {
		return nil, err
	}
	return &ConnectionBlocked{Reason: text}, nil
}

func ParseChannelFlow(payload []byte) (*ChannelFlow, error) {
	if len(payload) < 1 {
		return nil, fmt.Errorf("too short for channel flow")
	}
	// active is a bit (boolean)
	active := payload[0] != 0
	return &ChannelFlow{Active: active}, nil
}

func ParseConnectionTune(payload []byte) (*ConnectionTune, error) {
	if len(payload) < 8 {
		return nil, fmt.Errorf("too short for tune")
	}
	return &ConnectionTune{
		ChannelMax: binary.BigEndian.Uint16(payload[0:2]),
		FrameMax:   binary.BigEndian.Uint32(payload[2:6]),
		Heartbeat:  binary.BigEndian.Uint16(payload[6:8]),
	}, nil
}

func ParseBasicReturn(payload []byte) (*BasicReturn, error) {
	if len(payload) < 2 {
		return nil, fmt.Errorf("too short")
	}
	br := &BasicReturn{ReplyCode: binary.BigEndian.Uint16(payload[0:2])}
	text, offset, err := parseShortStr(payload, 2)
	if err != nil {
		return br, err
	}
	br.ReplyText = text
	exchange, offset, err := parseShortStr(payload, offset)
	if err != nil {
		return br, err
	}
	br.Exchange = exchange
	routingKey, _, err := parseShortStr(payload, offset)
	if err != nil {
		return br, err
	}
	br.RoutingKey = routingKey
	return br, nil
}

// ParseBasicPublish decodes basic.publish (class 60, method 40).
// Layout: ticket(short) + exchange(shortstr) + routing-key(shortstr) + bits(mandatory|immediate)
func ParseBasicPublish(payload []byte) (*BasicPublish, error) {
	// skip ticket (2 bytes, deprecated)
	_, offset, err := parseShortUint(payload, 0)
	if err != nil {
		return nil, err
	}
	exchange, offset, err := parseShortStr(payload, offset)
	if err != nil {
		return nil, err
	}
	routingKey, offset, err := parseShortStr(payload, offset)
	if err != nil {
		return nil, err
	}
	var mandatory, immediate bool
	if offset < len(payload) {
		bits := payload[offset]
		mandatory = bits&(1<<0) != 0
		immediate = bits&(1<<1) != 0
	}
	return &BasicPublish{Exchange: exchange, RoutingKey: routingKey, Mandatory: mandatory, Immediate: immediate}, nil
}

// ParseBasicConsume decodes basic.consume (class 60, method 20).
// Layout: ticket(short) + queue(shortstr) + consumer-tag(shortstr) + bits(no-local|no-ack|exclusive|no-wait)
func ParseBasicConsume(payload []byte) (*BasicConsume, error) {
	_, offset, err := parseShortUint(payload, 0) // skip ticket
	if err != nil {
		return nil, err
	}
	queue, offset, err := parseShortStr(payload, offset)
	if err != nil {
		return nil, err
	}
	consumerTag, offset, err := parseShortStr(payload, offset)
	if err != nil {
		return nil, err
	}
	var noAck, exclusive bool
	if offset < len(payload) {
		bits := payload[offset]
		// bit0=no-local, bit1=no-ack, bit2=exclusive, bit3=no-wait
		noAck = bits&(1<<1) != 0
		exclusive = bits&(1<<2) != 0
	}
	return &BasicConsume{Queue: queue, ConsumerTag: consumerTag, NoAck: noAck, Exclusive: exclusive}, nil
}

// ParseBasicCancel decodes basic.cancel (class 60, method 30).
// Layout: consumer-tag(shortstr) + no-wait(bit)
func ParseBasicCancel(payload []byte) (*BasicCancel, error) {
	tag, _, err := parseShortStr(payload, 0)
	if err != nil {
		return nil, err
	}
	return &BasicCancel{ConsumerTag: tag}, nil
}

// ParseBasicNack decodes basic.nack (class 60, method 120).
// Layout: delivery-tag(longlong) + bits(multiple|requeue)
func ParseBasicNack(payload []byte) (*BasicNack, error) {
	tag, offset, err := parseLongLongUint(payload, 0)
	if err != nil {
		return nil, err
	}
	var multiple, requeue bool
	if offset < len(payload) {
		bits := payload[offset]
		multiple = bits&(1<<0) != 0
		requeue = bits&(1<<1) != 0
	}
	return &BasicNack{DeliveryTag: tag, Multiple: multiple, Requeue: requeue}, nil
}

// ParseBasicReject decodes basic.reject (class 60, method 90).
// Layout: delivery-tag(longlong) + requeue(bit)
func ParseBasicReject(payload []byte) (*BasicReject, error) {
	tag, offset, err := parseLongLongUint(payload, 0)
	if err != nil {
		return nil, err
	}
	requeue := false
	if offset < len(payload) {
		requeue = payload[offset]&(1<<0) != 0
	}
	return &BasicReject{DeliveryTag: tag, Requeue: requeue}, nil
}

// ParseQueueDeclare decodes queue.declare (class 40, method 10).
// Layout: ticket(short) + queue(shortstr) + bits(passive|durable|exclusive|auto-delete|no-wait) + args(table)
func ParseQueueDeclare(payload []byte) (*QueueDeclare, error) {
	_, offset, err := parseShortUint(payload, 0) // skip ticket
	if err != nil {
		return nil, err
	}
	queue, offset, err := parseShortStr(payload, offset)
	if err != nil {
		return nil, err
	}
	qd := &QueueDeclare{Queue: queue}
	if offset < len(payload) {
		bits := payload[offset]
		qd.Passive = bits&(1<<0) != 0
		qd.Durable = bits&(1<<1) != 0
		qd.Exclusive = bits&(1<<2) != 0
		qd.AutoDelete = bits&(1<<3) != 0
	}
	return qd, nil
}

// ParseQueueDelete decodes queue.delete (class 40, method 40).
// Layout: ticket(short) + queue(shortstr) + bits(if-unused|if-empty|no-wait)
func ParseQueueDelete(payload []byte) (*QueueDelete, error) {
	_, offset, err := parseShortUint(payload, 0)
	if err != nil {
		return nil, err
	}
	queue, offset, err := parseShortStr(payload, offset)
	if err != nil {
		return nil, err
	}
	qd := &QueueDelete{Queue: queue}
	if offset < len(payload) {
		bits := payload[offset]
		qd.IfUnused = bits&(1<<0) != 0
		qd.IfEmpty = bits&(1<<1) != 0
	}
	return qd, nil
}

// ParseQueuePurge decodes queue.purge (class 40, method 30).
// Layout: ticket(short) + queue(shortstr) + no-wait(bit)
func ParseQueuePurge(payload []byte) (*QueuePurge, error) {
	_, offset, err := parseShortUint(payload, 0)
	if err != nil {
		return nil, err
	}
	queue, _, err := parseShortStr(payload, offset)
	if err != nil {
		return nil, err
	}
	return &QueuePurge{Queue: queue}, nil
}

// ParseExchangeDeclare decodes exchange.declare (class 30, method 10).
// Layout: ticket(short) + exchange(shortstr) + type(shortstr) + bits(passive|durable|auto-delete|internal|no-wait)
func ParseExchangeDeclare(payload []byte) (*ExchangeDeclare, error) {
	_, offset, err := parseShortUint(payload, 0)
	if err != nil {
		return nil, err
	}
	exchange, offset, err := parseShortStr(payload, offset)
	if err != nil {
		return nil, err
	}
	exchType, offset, err := parseShortStr(payload, offset)
	if err != nil {
		return nil, err
	}
	ed := &ExchangeDeclare{Exchange: exchange, Type: exchType}
	if offset < len(payload) {
		bits := payload[offset]
		ed.Passive = bits&(1<<0) != 0
		ed.Durable = bits&(1<<1) != 0
		ed.AutoDelete = bits&(1<<2) != 0
	}
	return ed, nil
}

// ParseExchangeDelete decodes exchange.delete (class 30, method 20).
// Layout: ticket(short) + exchange(shortstr) + bits(if-unused|no-wait)
func ParseExchangeDelete(payload []byte) (*ExchangeDelete, error) {
	_, offset, err := parseShortUint(payload, 0)
	if err != nil {
		return nil, err
	}
	exchange, offset, err := parseShortStr(payload, offset)
	if err != nil {
		return nil, err
	}
	ed := &ExchangeDelete{Exchange: exchange}
	if offset < len(payload) {
		ed.IfUnused = payload[offset]&(1<<0) != 0
	}
	return ed, nil
}

// skipTable skips a table value (4-byte length prefix + table bytes).
func skipTable(data []byte, offset int) (int, error) {
	size, offset, err := parseLongUint(data, offset)
	if err != nil {
		return offset, err
	}
	return offset + int(size), nil
}
