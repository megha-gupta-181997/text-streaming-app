package main

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/streadway/amqp"
)

type Provider struct {
	ID   int
	Data []string
}

var providers = []Provider{
	{ID: 1, Data: []string{"Hello from Provider 1", "How can I assist you?"}},
	{ID: 2, Data: []string{"Greetings from Provider 2", "What can I do for you today?"}},
	{ID: 3, Data: []string{"Hi from Provider 3", "Need any help?"}},
}

var currentProviderIndex = 0
var errorCount = 0

const errorThreshold = 3
const responseTimeThreshold = 2000 * time.Millisecond

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

var connectionPool = struct {
	sync.RWMutex
	connections map[*websocket.Conn]bool
}{connections: make(map[*websocket.Conn]bool)}

var rabbitConn *amqp.Connection
var rabbitChannel *amqp.Channel

func main() {
	var err error
	rabbitConn, err = amqp.Dial("amqp://guest:guest@localhost:5672/")
	if err != nil {
		log.Fatalf("Failed to connect to RabbitMQ: %v", err)
	}
	defer rabbitConn.Close()

	rabbitChannel, err = rabbitConn.Channel()
	if err != nil {
		log.Fatalf("Failed to open a channel: %v", err)
	}
	defer rabbitChannel.Close()

	_, err = rabbitChannel.QueueDeclare(
		"text_stream_queue",
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		log.Fatalf("Failed to declare a queue: %v", err)
	}

	go monitorProviders()

	http.HandleFunc("/ws", handleConnections)
	http.HandleFunc("/health", handleHealthCheck)
	log.Println("Server started on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleConnections(w http.ResponseWriter, r *http.Request) {
	upgrader.CheckOrigin = func(r *http.Request) bool { return true }
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatalf("Failed to upgrade connection: %v", err)
	}
	defer ws.Close()

	connectionPool.Lock()
	connectionPool.connections[ws] = true
	connectionPool.Unlock()

	defer func() {
		connectionPool.Lock()
		delete(connectionPool.connections, ws)
		connectionPool.Unlock()
	}()

	for {
		_, msg, err := ws.ReadMessage()
		if err != nil {
			log.Printf("Read error: %v", err)
			break
		}
		log.Printf("Received: %s", msg)

		err = rabbitChannel.Publish(
			"",
			"text_stream_queue",
			false,
			false,
			amqp.Publishing{
				ContentType: "text/plain",
				Body:        []byte(msg),
			},
		)
		if err != nil {
			log.Printf("Failed to publish message: %v", err)
			continue
		}

		response, err := simulateProviderResponse(providers[currentProviderIndex])
		if err != nil {
			if errorCount >= errorThreshold {
				currentProviderIndex = (currentProviderIndex + 1) % len(providers)
				errorCount = 0
				ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("Switched to Provider %d due to errors", currentProviderIndex+1)))
			} else {
				ws.WriteMessage(websocket.TextMessage, []byte(err.Error()))
			}
		} else {
			ws.WriteMessage(websocket.TextMessage, []byte(response))
		}
	}
}

func simulateProviderResponse(provider Provider) (string, error) {
	delay := time.Duration(rand.Intn(3000)) * time.Millisecond
	time.Sleep(delay)

	if delay > responseTimeThreshold {
		errorCount++
		return "", fmt.Errorf("response time exceeded")
	}

	response := provider.Data[rand.Intn(len(provider.Data))]
	return response, nil
}

func monitorProviders() {
	for {
		time.Sleep(10 * time.Second)
		log.Println("Monitoring providers...")
	}
}

func handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
