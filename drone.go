package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/gofiber/fiber/v2"
	"github.com/gorilla/websocket"
)

type Location struct {
	ID        int     `json:"id"`
	Name      string  `json:"name"`
	Latitude  float64 `json:"lat"`
	Longitude float64 `json:"lon"`
	Altitude  float64 `json:"alt"`
	State     string  `json:"state"`
	Battery   float64 `json:"batt"`
	speed     float64 `json:"-"`
}

type FlightPattern struct {
	PathType      string     `json:"path"`
	Properties    string     `json:"props"`
	StartLocation Location   `json:"start"`
	EndLocation   Location   `json:"end"`
	Points        []Location `json:"points"`
	Data          []float64  `json:"data"`
}

var ctx = context.Background()
var drone Location

var redisClient *redis.Client

func broadcast(wsHeading chan Location) {
	data, err := json.Marshal(drone)
	if err != nil {
		panic(err)
	}
	if err := redisClient.Publish(ctx, fmt.Sprintf("drone-%d", drone.ID), data).Err(); err != nil {
		panic(err)
	}
	wsHeading <- drone
	time.Sleep(2 * time.Second) // pauses execution for 2 seconds
	drone.Battery -= 0.01
}

func waypoint(dest chan Location, wsHeading chan Location) {
	for whereTo := range dest {
		dY, dX := whereTo.Latitude-drone.Latitude, whereTo.Longitude-drone.Longitude
		norm := math.Sqrt(dX*dX + dY + dY)
		dY = dY / norm
		dX = dX / norm
		for norm > drone.speed {
			drone.Latitude += dY * drone.speed
			drone.Longitude += dX * drone.speed
			dY, dX = whereTo.Latitude-drone.Latitude, whereTo.Longitude-drone.Longitude
			norm = math.Sqrt(dX*dX + dY + dY)
			broadcast(wsHeading)
		}
		drone = Location{ID: drone.ID, speed: drone.speed, Latitude: whereTo.Latitude, Longitude: whereTo.Longitude, State: "Flying", Altitude: whereTo.Altitude}
		broadcast(wsHeading)
	}
}

func pattern(dest chan Location, start *Location, end *Location, left *Location, right *Location, mmNode float64, mNode float64, speed float64) {
	lastX, lastY := 0.0, 0.0
	if left == nil || right == nil {
		a := (end.Longitude - start.Longitude) / 2
		b := (end.Latitude - start.Latitude) / 2
		mx := start.Longitude + a
		my := start.Latitude + b
		left = &Location{Latitude: my + a/2, Longitude: mx - b/2}
		right = &Location{Latitude: my - a/2, Longitude: mx + b/2}
	}
	for i := 1; i < 360; i++ {
		mmx, mmy := end.Longitude-start.Longitude, end.Latitude-start.Latitude
		mx, my := left.Longitude-right.Longitude, left.Latitude-right.Latitude
		xx := start.Longitude + math.Sin(mmNode*float64(i)*3.14159/180.0)*mmx + math.Cos(mNode*float64(i)*3.14159/180.0)*mx
		yy := start.Latitude + math.Sin(mmNode*float64(i)*3.14159/180.0)*mmy + math.Cos(mNode*float64(i)*3.14159/180.0)*my
		if (lastX-xx)*(lastX-xx)+(lastY-yy)*(lastY-yy) > speed*speed {
			dest <- Location{ID: drone.ID, speed: speed, Latitude: yy, Longitude: xx}
			lastX, lastY = xx, yy
		}
	}
}

func navigate(course chan FlightPattern, dest chan Location) {
	for flight := range course {
		dest <- flight.StartLocation // first fly to initial point
		var left, right *Location
		if len(flight.Points) > 0 {
			left = &flight.Points[0]
			if len(flight.Points) > 1 {
				right = &flight.Points[1]
			}
		}
		mmLoops, mLoops := 0.0, 0.0
		if len(flight.Data) > 0 {
			mmLoops = flight.Data[0]
			if len(flight.Data) > 1 {
				mLoops = flight.Data[1]
			}
		}
		switch flight.PathType {
		case "sine":
			for len(course) == 0 {
				pattern(dest, &flight.StartLocation, &flight.EndLocation, left, right, mmLoops, mLoops, flight.StartLocation.speed)
			}
			dest <- flight.EndLocation // fly to extraction point before next objective
		case "figure8":
			for len(course) == 0 {
				pattern(dest, &flight.StartLocation, &flight.EndLocation, left, right, 1, 2, flight.StartLocation.speed)
			}
		case "clockwise":
			for len(course) == 0 {
				pattern(dest, &flight.StartLocation, &flight.EndLocation, left, right, 1, 1, flight.StartLocation.speed)
			}
		case "counter-clockwise":
			for len(course) == 0 {
				pattern(dest, &flight.StartLocation, &flight.EndLocation, right, left, 1, -1, flight.StartLocation.speed)
			}
		case "patrol":
			for len(course) == 0 { // as long as there are no other orders, keep patroling
				for _, wp := range flight.Points {
					dest <- Location{ID: drone.ID, speed: wp.speed, Latitude: wp.Latitude, Longitude: wp.Longitude}
				}
			}
			dest <- flight.EndLocation // fly to extraction point before next objective
		case "random":
		default:
			dest <- flight.EndLocation
		}
	}
}

func speak(conn *websocket.Conn, wsHeading chan Location) {
	for drone := range wsHeading {

		if conn != nil {
			data, err := json.Marshal(drone)
			if err != nil {
				panic(err)
			}
			if err := conn.WriteMessage(1, []byte(data)); err != nil {
				log.Println(err)
				return
			}
		}
	}
}

func listen(conn *websocket.Conn) {
	for {
		// read a message
		messageType, messageContent, err := conn.ReadMessage()
		timeReceive := time.Now()
		if err != nil {
			log.Println(err)
			return
		}

		// print out that message
		fmt.Println(string(messageContent))

		// reponse message
		messageResponse := fmt.Sprintf("Your message is: %s. Time received : %v", messageContent, timeReceive)

		if err := conn.WriteMessage(messageType, []byte(messageResponse)); err != nil {
			log.Println(err)
			return
		}
	}
}

func websocketInit(socket string, wsHeading chan Location) {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {

		upgrader.CheckOrigin = func(r *http.Request) bool { return true }

		websocket, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println(err)
			return
		}
		log.Println("Websocket Moniton Connected!")

		go speak(websocket, wsHeading)
		listen(websocket)
	})

	http.ListenAndServe(":"+socket, nil)
}

func main() {

	listen := flag.String("listen", "3000", "port to listen for commands")
	socket := flag.String("socket", "3070", "port to push drone data")
	name := flag.String("name", "drone", "name of drone")
	id := flag.Int("id", 9, "drone ID")
	topSpeed := flag.Float64("top-speed", 0.000129726, "max speed of drone")
	latitude := flag.Float64("latitude", -33.937687, "latitude location of drone")
	longitude := flag.Float64("longitude", 151.19189864, "longitude location of drone")
	redisHost := flag.String("redis", "localhost:6379", "Address of redis host")

	redisClient = redis.NewClient(&redis.Options{
		Addr: *redisHost,
	})

	flag.Parse()

	drone = Location{ID: *id, Name: *name, speed: *topSpeed, Latitude: *latitude, Longitude: *longitude, Battery: 10800.0, State: "Ready"}

	app := fiber.New()

	heading := make(chan Location)
	wsHeading := make(chan Location)
	go waypoint(heading, wsHeading)
	order := make(chan FlightPattern, 2)
	go navigate(order, heading)

	app.Post("/fly", func(c *fiber.Ctx) error {
		fp := new(FlightPattern)

		if err := c.BodyParser(fp); err != nil {
			panic(err)
		}
		order <- *fp
		return c.SendStatus(200)
	})

	go websocketInit(*socket, wsHeading)

	app.Listen(":" + *listen)
}
