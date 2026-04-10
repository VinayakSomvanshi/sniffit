package rules

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/vinayak/sniffit/internal/amqp"
)

// Engine processes parsed AMQP frames.
// It returns an Alert for EVERY meaningful method frame observed — not just
// rule violations. Severity is one of "error", "warn", or "info".
type Engine struct{}

func NewEngine() *Engine { return &Engine{} }

// infoAlert creates a low-severity informational event for normal lifecycle traffic.
func infoAlert(ruleID, ruleName, methodName, entity, plain string) *Alert {
	return &Alert{
		Severity:     "info",
		RuleID:       ruleID,
		RuleName:     ruleName,
		MethodName:   methodName,
		Entity:       entity,
		PlainEnglish: plain,
		Timestamp:    time.Now().UTC(),
	}
}

func warnAlert(ruleID, ruleName, methodName, entity, plain string) *Alert {
	return &Alert{
		Severity:     "warn",
		RuleID:       ruleID,
		RuleName:     ruleName,
		MethodName:   methodName,
		Entity:       entity,
		PlainEnglish: plain,
		Timestamp:    time.Now().UTC(),
	}
}

func errAlert(ruleID, ruleName, methodName, entity, plain string) *Alert {
	return &Alert{
		Severity:     "error",
		RuleID:       ruleID,
		RuleName:     ruleName,
		MethodName:   methodName,
		Entity:       entity,
		PlainEnglish: plain,
		Timestamp:    time.Now().UTC(),
	}
}

// EvaluateMethod evaluates an AMQP Method Payload and returns an Alert for
// every observable event. Returns nil only for high-frequency ack/deliver
// frames that would create too much noise.
func (e *Engine) EvaluateMethod(m *amqp.MethodPayload) *Alert {
	mn := amqp.MethodName(m.ClassID, m.MethodID)
	log.Printf("[rules] evaluate %s (class=%d method=%d)", mn, m.ClassID, m.MethodID)

	switch m.ClassID {

	// ── Class 10: Connection ─────────────────────────────────────────────────
	case 10:
		switch m.MethodID {
		case 10: // connection.start
			return infoAlert("0.1", "connection_negotiation", mn, "", "New AMQP connection handshake started (connection.start received from broker).")

		case 30: // connection.tune
			ct, err := amqp.ParseConnectionTune(m.Args)
			if err != nil {
				return infoAlert("0.2", "connection_tune", mn, "", "Connection parameters negotiated (could not parse details).")
			}
			return e.evaluateConnectionTune(ct, mn)

		case 40: // connection.open
			return infoAlert("0.3", "connection_opened", mn, "", "AMQP connection opened successfully.")

		case 41: // connection.open-ok
			return infoAlert("0.3", "connection_opened", mn, "", "Broker confirmed connection open.")

		case 50: // connection.close
			cc, err := amqp.ParseConnectionClose(m.Args)
			if err != nil {
				return warnAlert("0.x", "connection_close_unparseable", mn, "", "Connection closed — could not parse close frame.")
			}
			return e.evaluateConnectionClose(cc, mn)

		case 51: // connection.close-ok
			return infoAlert("0.4", "connection_closed_ok", mn, "", "Connection closed cleanly (close-ok handshake complete).")

		case 60: // connection.blocked
			cb, err := amqp.ParseConnectionBlocked(m.Args)
			if err != nil {
				return warnAlert("2.0", "connection_blocked", mn, "", "Connection is blocked by broker (could not parse reason).")
			}
			return e.evaluateConnectionBlocked(cb, mn)

		case 61: // connection.unblocked
			return infoAlert("2.0", "connection_unblocked", mn, "", "Connection unblocked — broker resource pressure has eased.")
		}

	// ── Class 20: Channel ────────────────────────────────────────────────────
	case 20:
		switch m.MethodID {
		case 10: // channel.open
			return infoAlert("0.5", "channel_opened", mn, "", "New AMQP channel opened by client.")

		case 11: // channel.open-ok
			return infoAlert("0.5", "channel_opened", mn, "", "Broker confirmed channel open.")

		case 20: // channel.flow
			cf, err := amqp.ParseChannelFlow(m.Args)
			if err != nil {
				return warnAlert("2.5", "channel_flow_unparseable", mn, "", "Broker requested flow control adjustment (could not parse).")
			}
			if !cf.Active {
				return errAlert("2.5", "channel_flow_throttled", mn, "", "Broker actively throttled publishers via channel.flow (active=false). The broker is struggling under load.")
			}
			return infoAlert("2.5", "channel_flow_resumed", mn, "", "Broker resumed publisher flow (active=true).")

		case 40: // channel.close
			cc, err := amqp.ParseChannelClose(m.Args)
			if err != nil {
				return warnAlert("0.x", "channel_close_unparseable", mn, "", "Channel closed — could not parse close frame.")
			}
			return e.evaluateChannelClose(cc, mn)

		case 41: // channel.close-ok
			return infoAlert("0.6", "channel_closed_ok", mn, "", "Channel closed cleanly (close-ok handshake complete).")
		}

	// ── Class 30: Exchange ───────────────────────────────────────────────────
	case 30:
		switch m.MethodID {
		case 10: // exchange.declare
			ed, err := amqp.ParseExchangeDeclare(m.Args)
			if err != nil {
				return infoAlert("0.7", "exchange_declare", mn, "", "Exchange declared (could not parse details).")
			}
			flags := ""
			if ed.Durable {
				flags += " durable"
			}
			if ed.AutoDelete {
				flags += " auto-delete"
			}
			return infoAlert("0.7", "exchange_declare", mn, ed.Exchange,
				fmt.Sprintf("Exchange declared: name=%q type=%s%s.", ed.Exchange, ed.Type, flags))

		case 11: // exchange.declare-ok
			return infoAlert("0.7", "exchange_declare", mn, "", "Broker confirmed exchange declaration.")

		case 20: // exchange.delete
			ed, err := amqp.ParseExchangeDelete(m.Args)
			if err != nil {
				return warnAlert("0.8", "exchange_deleted", mn, "", "Exchange deleted (could not parse details).")
			}
			return warnAlert("0.8", "exchange_deleted", mn, ed.Exchange,
				fmt.Sprintf("Exchange deleted: name=%q.", ed.Exchange))

		case 21: // exchange.delete-ok
			return infoAlert("0.8", "exchange_deleted", mn, "", "Broker confirmed exchange deletion.")

		case 30: // exchange.bind
			return infoAlert("0.9", "exchange_bind", mn, "", "Exchange-to-exchange binding declared.")
		case 40: // exchange.unbind
			return infoAlert("0.9", "exchange_unbind", mn, "", "Exchange-to-exchange binding removed.")
		}

	// ── Class 40: Queue ──────────────────────────────────────────────────────
	case 40:
		switch m.MethodID {
		case 10: // queue.declare
			qd, err := amqp.ParseQueueDeclare(m.Args)
			if err != nil {
				return infoAlert("0.10", "queue_declare", mn, "", "Queue declared (could not parse details).")
			}
			if qd.Passive {
				return infoAlert("0.10", "queue_declare", mn, qd.Queue,
					fmt.Sprintf("Queue existence check (passive declare): name=%q.", qd.Queue))
			}
			flags := fmt.Sprintf("durable=%v exclusive=%v auto-delete=%v", qd.Durable, qd.Exclusive, qd.AutoDelete)
			return infoAlert("0.10", "queue_declare", mn, qd.Queue,
				fmt.Sprintf("Queue declared: name=%q %s.", qd.Queue, flags))

		case 11: // queue.declare-ok
			return infoAlert("0.10", "queue_declare", mn, "", "Broker confirmed queue declaration.")

		case 20: // queue.bind
			return infoAlert("0.11", "queue_bind", mn, "", "Queue binding declared (queue bound to exchange with routing key).")

		case 21: // queue.bind-ok
			return infoAlert("0.11", "queue_bind", mn, "", "Broker confirmed queue binding.")

		case 30: // queue.purge
			qp, err := amqp.ParseQueuePurge(m.Args)
			if err != nil {
				return warnAlert("0.12", "queue_purged", mn, "", "Queue purged (could not parse queue name).")
			}
			return warnAlert("0.12", "queue_purged", mn, qp.Queue,
				fmt.Sprintf("Queue purged: ALL messages in %q were deleted. Verify this was intentional.", qp.Queue))

		case 31: // queue.purge-ok
			return infoAlert("0.12", "queue_purged", mn, "", "Broker confirmed queue purge.")

		case 40: // queue.delete
			qd, err := amqp.ParseQueueDelete(m.Args)
			if err != nil {
				return warnAlert("0.13", "queue_deleted", mn, "", "Queue deleted (could not parse details).")
			}
			return warnAlert("0.13", "queue_deleted", mn, qd.Queue,
				fmt.Sprintf("Queue deleted: name=%q if-unused=%v if-empty=%v.", qd.Queue, qd.IfUnused, qd.IfEmpty))

		case 50: // queue.unbind
			return infoAlert("0.11", "queue_unbind", mn, "", "Queue binding removed.")
		}

	// ── Class 60: Basic ──────────────────────────────────────────────────────
	case 60:
		switch m.MethodID {
		case 10: // basic.qos
			return infoAlert("0.14", "basic_qos", mn, "", "Consumer prefetch (QoS) configured.")

		case 20: // basic.consume
			bc, err := amqp.ParseBasicConsume(m.Args)
			if err != nil {
				return infoAlert("0.15", "consumer_registered", mn, "", "New consumer registered (could not parse details).")
			}
			exclusive := ""
			if bc.Exclusive {
				exclusive = " (exclusive)"
			}
			return infoAlert("0.15", "consumer_registered", mn, bc.Queue,
				fmt.Sprintf("Consumer registered on queue %q, tag=%q, no-ack=%v%s.", bc.Queue, bc.ConsumerTag, bc.NoAck, exclusive))

		case 21: // basic.consume-ok
			return infoAlert("0.15", "consumer_registered", mn, "", "Broker confirmed consumer registration.")

		case 30: // basic.cancel
			bc, err := amqp.ParseBasicCancel(m.Args)
			if err != nil {
				return warnAlert("0.16", "consumer_cancelled", mn, "", "Consumer cancelled (could not parse consumer tag).")
			}
			return warnAlert("0.16", "consumer_cancelled", mn, bc.ConsumerTag,
				fmt.Sprintf("Consumer cancelled: tag=%q. Check whether this was expected or caused by an application crash.", bc.ConsumerTag))

		case 40: // basic.publish
			bp, err := amqp.ParseBasicPublish(m.Args)
			if err != nil {
				return infoAlert("0.17", "message_published", mn, "", "Message published (could not parse details).")
			}
			target := bp.Exchange
			if target == "" {
				target = "(default exchange)"
			}
			flags := ""
			if bp.Mandatory {
				flags += " mandatory"
			}
			if bp.Immediate {
				flags += " immediate"
			}
			return infoAlert("0.17", "message_published", mn, fmt.Sprintf("%s|%s", bp.Exchange, bp.RoutingKey),
				fmt.Sprintf("Message published to exchange=%q routing-key=%q%s.", target, bp.RoutingKey, flags))

		case 50: // basic.return
			br, err := amqp.ParseBasicReturn(m.Args)
			if err != nil {
				return errAlert("3.2", "unroutable_message", mn, "", "Message returned unrouted (could not parse details).")
			}
			return e.evaluateBasicReturn(br, mn)

		case 90: // basic.reject
			br, err := amqp.ParseBasicReject(m.Args)
			if err != nil {
				return warnAlert("4.3", "message_rejected", mn, "", "Message explicitly rejected by consumer (could not parse details).")
			}
			requeue := "will be requeued"
			if !br.Requeue {
				requeue = "will NOT be requeued (dead-lettered or dropped)"
			}
			return warnAlert("4.3", "message_rejected", mn, "",
				fmt.Sprintf("Consumer rejected delivery-tag=%d — %s. Check your consumer error handling.", br.DeliveryTag, requeue))

		case 120: // basic.nack
			bn, err := amqp.ParseBasicNack(m.Args)
			if err != nil {
				return warnAlert("4.4", "message_nacked", mn, "", "Message negatively acknowledged (could not parse details).")
			}
			multiple := ""
			if bn.Multiple {
				multiple = " (multiple)"
			}
			requeue := "will be requeued"
			if !bn.Requeue {
				requeue = "will NOT be requeued"
			}
			return warnAlert("4.4", "message_nacked", mn, "",
				fmt.Sprintf("NACK on delivery-tag=%d%s. If from a consumer: processing failure (%s). If from the broker (Publisher Confirms): DATA LOSS WARNING, the broker failed to safely store the message.", bn.DeliveryTag, multiple, requeue))

		// High-frequency frames — skip to avoid noise
		case 60: // basic.deliver — high volume, skip individual events
			return nil
		case 80: // basic.ack — high volume, skip individual events
			return nil
		case 70, 71, 72: // basic.get, get-ok, get-empty
			return nil
		case 11: // basic.qos-ok
			return nil
		}

	// ── Class 90: Tx ─────────────────────────────────────────────────────────
	case 90:
		switch m.MethodID {
		case 10:
			return infoAlert("0.20", "tx_begin", mn, "", "AMQP transaction started (tx.select).")
		case 20:
			return infoAlert("0.20", "tx_commit", mn, "", "AMQP transaction committed.")
		case 30:
			return warnAlert("0.21", "tx_rollback", mn, "", "AMQP transaction rolled back — a transactional publish or ack failed.")
		}
	}

	// Unknown class/method — log and skip
	log.Printf("[rules] Unknown AMQP method: class=%d method=%d — skipping", m.ClassID, m.MethodID)
	return nil
}

// EvaluateHTTPResponse evaluates an HTTP response on the management port.
func (e *Engine) EvaluateHTTPResponse(resp *http.Response) *Alert {
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return errAlert("5.1", "management_auth_failure", "http.response", "",
			fmt.Sprintf("Management Auth Failure: HTTP %d on port 15672. An unauthorized attempt was made to access the RabbitMQ Management API. This may indicate a brute-force attempt or leaked credentials.", resp.StatusCode))
	}
	// Log other non-2xx responses as warnings
	if resp.StatusCode >= 400 {
		return warnAlert("5.2", "management_api_error", "http.response", "",
			fmt.Sprintf("Management API returned HTTP %d — an unexpected error from the RabbitMQ Management API.", resp.StatusCode))
	}
	return nil
}

// ── Specific rule evaluators ────────────────────────────────────────────────

func (e *Engine) evaluateConnectionClose(cc *amqp.ConnectionClose, mn string) *Alert {
	// Rule 1.1: Authentication refused
	if cc.ReplyCode == 403 {
		return errAlert("1.1", "authentication_refused", mn, "",
			"Authentication failed: the application is using invalid credentials, or is attempting to connect to a vhost that does not exist or that it lacks permissions for.")
	}
	// Rule 1.2: Missed heartbeats
	if cc.ReplyCode == 320 && strings.Contains(strings.ToLower(cc.ReplyText), "heartbeat") {
		return errAlert("1.2", "missed_heartbeats", mn, "",
			"Heartbeat timeout: the broker dropped the connection. If pod CPU is elevated, the application thread is blocked and cannot send heartbeats. If CPU is normal, suspect a network partition.")
	}
	// Rule 1.3: Connection limit
	if cc.ReplyCode == 320 && strings.Contains(strings.ToLower(cc.ReplyText), "connection limit") {
		return errAlert("1.3", "connection_limit_exceeded", mn, "",
			"Connection limit reached: the broker or vhost has hit its maximum allowed connections. Inspect the application for connection leaks.")
	}
	// Rule 3.4: Frame size
	if cc.ReplyCode == 501 {
		return errAlert("3.4", "frame_size_exceeded", mn, "",
			"Payload too large: the application attempted to send a message exceeding the broker's max_frame_size limit.")
	}
	// Rule 4.1: Consumer ACK timeout
	if cc.ReplyCode == 406 && strings.Contains(strings.ToLower(cc.ReplyText), "delivery acknowledgement") {
		return errAlert("4.1", "consumer_acknowledgement_timeout", mn, "",
			fmt.Sprintf("Consumer timeout: the application took too long to acknowledge a message. RabbitMQ closed the channel. Hint: %s", cc.ReplyText))
	}
	// Clean close by application (200 = normal)
	if cc.ReplyCode == 200 {
		return infoAlert("0.4", "connection_closed_cleanly", mn, "",
			fmt.Sprintf("Connection closed cleanly by application (%d: %s).", cc.ReplyCode, cc.ReplyText))
	}
	// Catch-all: any other close code is noteworthy
	sev := "warn"
	if cc.ReplyCode >= 500 {
		sev = "error"
	}
	return &Alert{
		Severity:     sev,
		RuleID:       "1.x",
		RuleName:     "connection_closed_unexpectedly",
		MethodName:   mn,
		PlainEnglish: fmt.Sprintf("Connection closed with code %d: %q. This is an unclassified broker-side close. Investigate the broker logs for more context.", cc.ReplyCode, cc.ReplyText),
		Timestamp:    time.Now().UTC(),
	}
}

func (e *Engine) evaluateConnectionTune(ct *amqp.ConnectionTune, mn string) *Alert {
	// Rule 1.4: Heartbeat override
	if ct.Heartbeat > 60 {
		return warnAlert("1.4", "connection_tune_override", mn, "",
			fmt.Sprintf("Configuration Warning: The server negotiated a heartbeat interval of %ds. This usually indicates the broker has clamped the client's requested value.", ct.Heartbeat))
	}
	return infoAlert("0.2", "connection_tune", mn, "",
		fmt.Sprintf("Connection parameters negotiated: channel-max=%d frame-max=%d heartbeat=%ds.", ct.ChannelMax, ct.FrameMax, ct.Heartbeat))
}

func (e *Engine) evaluateConnectionBlocked(cb *amqp.ConnectionBlocked, mn string) *Alert {
	reason := strings.ToLower(cb.Reason)
	if strings.Contains(reason, "memory") {
		return warnAlert("2.1", "memory_watermark_alarm", mn, "",
			"Publishing blocked (memory): RabbitMQ has breached its memory threshold and is blocking publishers to prevent a crash. Consumers continue to drain.")
	}
	if strings.Contains(reason, "disk") {
		return warnAlert("2.2", "disk_watermark_alarm", mn, "",
			"Publishing blocked (disk): the broker node has exhausted its free disk space. Publishers are paused; consumers can still drain queues.")
	}
	return warnAlert("2.0", "connection_blocked", mn, "",
		fmt.Sprintf("Connection blocked by broker — reason: %q.", cb.Reason))
}

func (e *Engine) evaluateChannelClose(cc *amqp.ChannelClose, mn string) *Alert {
	// Rule 2.3: Channel limit
	if cc.ReplyCode == 503 {
		return errAlert("2.3", "channel_limit_exceeded", mn, "",
			"Channel exhaustion: the application opened more channels than the broker's negotiated channel_max permits. Inspect the application for channel leaks.")
	}
	// Rule 3.1: Topology mismatch
	if cc.ReplyCode == 406 {
		return errAlert("3.1", "topology_mismatch", mn, "",
			fmt.Sprintf("Topology mismatch: the application declared an entity with parameters conflicting with the existing broker definition. Hint: %s", cc.ReplyText))
	}
	// Rule 2.4: Resource Locked (Exclusive Queue Violation)
	if cc.ReplyCode == 405 {
		return errAlert("2.4", "resource_locked", mn, "",
			fmt.Sprintf("Resource Locked: A client attempted to work with an exclusive queue or resource that is already locked by another connection. Hint: %s", cc.ReplyText))
	}
	// Rule 3.3: Entity not found
	if cc.ReplyCode == 404 {
		return errAlert("3.3", "entity_not_found", mn, "",
			fmt.Sprintf("Routing error: the application tried to publish to an exchange or consume from a queue that does not exist. Hint: %s", cc.ReplyText))
	}
	// Rule 3.4: Frame error
	if cc.ReplyCode == 501 {
		return errAlert("3.4", "frame_size_exceeded", mn, "",
			"Payload too large: the application attempted to send a message exceeding the broker's max_frame_size limit.")
	}
	// Rule 4.1: Consumer ACK timeout at channel level
	if cc.ReplyCode == 406 && strings.Contains(strings.ToLower(cc.ReplyText), "delivery acknowledgement") {
		return errAlert("4.1", "consumer_acknowledgement_timeout", mn, "",
			fmt.Sprintf("Consumer timeout: the application took too long to acknowledge a message. Hint: %s", cc.ReplyText))
	}
	// Clean close
	if cc.ReplyCode == 200 {
		return infoAlert("0.6", "channel_closed_cleanly", mn, "",
			fmt.Sprintf("Channel closed cleanly (%d: %s).", cc.ReplyCode, cc.ReplyText))
	}
	// Catch-all for any other channel close code
	sev := "warn"
	if cc.ReplyCode >= 500 {
		sev = "error"
	}
	return &Alert{
		Severity:     sev,
		RuleID:       "3.x",
		RuleName:     "channel_closed_unexpectedly",
		MethodName:   mn,
		PlainEnglish: fmt.Sprintf("Channel closed with code %d: %q. Unclassified channel-level error.", cc.ReplyCode, cc.ReplyText),
		Timestamp:    time.Now().UTC(),
	}
}

func (e *Engine) evaluateBasicReturn(br *amqp.BasicReturn, mn string) *Alert {
	// Rule 3.2: Unroutable message
	if br.ReplyCode == 312 || br.ReplyCode == 313 {
		return warnAlert("3.2", "unroutable_message", mn, fmt.Sprintf("%s|%s", br.Exchange, br.RoutingKey),
			fmt.Sprintf("Message dropped: a mandatory-flagged message to exchange=%q routing-key=%q had no matching queue binding. The message was returned to the publisher.", br.Exchange, br.RoutingKey))
	}
	return warnAlert("3.2", "message_returned", mn, fmt.Sprintf("%s|%s", br.Exchange, br.RoutingKey),
		fmt.Sprintf("Message returned: exchange=%q routing-key=%q code=%d reason=%q.", br.Exchange, br.RoutingKey, br.ReplyCode, br.ReplyText))
}
