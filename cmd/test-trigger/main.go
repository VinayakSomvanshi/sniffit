package main

import (
	"context"
	"flag"
	"log"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

func main() {
	url := flag.String("url", "amqp://guest:guest@localhost:5672/", "AMQP connection string")
	flag.Parse()

	log.Println("=== SniffIt Production Simulator ===")
	log.Println("This script will generate normal traffic, warnings, and errors to populate the SniffIt dashboard.")

	// 1. Authentication Failure (Rule 1.1)
	log.Println("\n[1/7] Generating Error: Authentication Failure (Rule 1.1)")
	_, err := amqp.Dial("amqp://baduser:badpass@localhost:5672/")
	if err != nil {
		log.Printf("  -> Success: Got expected auth error: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Connect validly for the rest
	log.Printf("\nConnecting validly to %s...", *url)
	conn, err := amqp.Dial(*url)
	if err != nil {
		log.Fatalf("  -> Failed to connect: %v", err)
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		log.Fatalf("  -> Failed to open a channel: %v", err)
	}

	queueName := "sniffit-prod-q-" + time.Now().Format("150405")
	exchangeName := "sniffit-prod-ex-" + time.Now().Format("150405")

	// 2. Normal Topology Creation (Info events)
	log.Println("\n[2/7] Generating Info: Creating Topology (Queues, Exchanges, Bindings)")
	err = ch.ExchangeDeclare(exchangeName, "direct", true, false, false, false, nil)
	if err == nil {
		log.Printf("  -> Declared exchange: %s", exchangeName)
	}
	_, err = ch.QueueDeclare(queueName, true, false, false, false, nil)
	if err == nil {
		log.Printf("  -> Declared queue: %s", queueName)
	}
	err = ch.QueueBind(queueName, "my-key", exchangeName, false, nil)
	if err == nil {
		log.Printf("  -> Bound queue to exchange")
	}
	time.Sleep(500 * time.Millisecond)

	// 3. Topology Mismatch (Rule 3.1)
	log.Println("\n[3/7] Generating Error: Topology Mismatch (Rule 3.1)")
	// Try creating the EXACT SAME queue, but non-durable instead of durable
	_, err = ch.QueueDeclare(queueName, false, false, false, false, nil)
	if err != nil {
		log.Printf("  -> Success: Got expected mismatch error: %v", err)
		// Channel is closed by the server on error, we must open a new one
		ch, _ = conn.Channel()
	}
	time.Sleep(500 * time.Millisecond)

	// 4. Unroutable Message (Rule 3.2 - Warning)
	log.Println("\n[4/7] Generating Warning: Unroutable Message (Rule 3.2)")
	// We must register a return listener to actually process returned mandatory messages cleanly
	returns := ch.NotifyReturn(make(chan amqp.Return, 1))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err = ch.PublishWithContext(ctx, exchangeName, "wrong-key", true, false, amqp.Publishing{
		ContentType: "text/plain",
		Body:        []byte("This message will be returned"),
	})
	if err == nil {
		<-returns // Wait for the server to return it
		log.Printf("  -> Success: Message was returned (Unroutable)")
	}
	time.Sleep(500 * time.Millisecond)

	// 5. Normal Publish & Consume with Rejects (Rule 4.3 - Warning)
	log.Println("\n[5/7] Generating Warning: Message Rejected (Rule 4.3)")
	ch.PublishWithContext(context.Background(), exchangeName, "my-key", false, false, amqp.Publishing{
		ContentType: "text/plain", Body: []byte("Bad Data"),
	})
	
	msgs, err := ch.Consume(queueName, "sniffit-tester-tag", false, false, false, false, nil)
	if err == nil {
		msg := <-msgs
		err = msg.Reject(false) // Reject it
		if err == nil {
			log.Printf("  -> Success: Message rejected by consumer")
		}
	}
	time.Sleep(500 * time.Millisecond)

	// 6. Normal Publish & Nack (Rule 4.4 - Warning)
	log.Println("\n[6/7] Generating Warning: Message Nack (Rule 4.4)")
	ch.PublishWithContext(context.Background(), exchangeName, "my-key", false, false, amqp.Publishing{
		ContentType: "text/plain", Body: []byte("Poison Pill"),
	})
	// Still subscribed, grab the next one
	msg2 := <-msgs
	err = msg2.Nack(false, false) // Nack it
	if err == nil {
		log.Printf("  -> Success: Message NACKed by consumer")
	}
	time.Sleep(500 * time.Millisecond)
	ch.Cancel("sniffit-tester-tag", false)

	// 7. Entity Not Found (Rule 3.3 - Error)
	log.Println("\n[7/7] Generating Error: Entity Not Found (Rule 3.3)")
	_, err = ch.Consume("some-missing-queue", "", true, false, false, false, nil)
	if err != nil {
		log.Printf("  -> Success: Got expected missing queue error: %v", err)
	}

	log.Println("\n=== Simulation Complete ===")
	log.Println("Check the SniffIt dashboard! If running locally, visit http://localhost:8080")
}
