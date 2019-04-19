package main

import (
	"flag"
	"log"
	"net/url"
	"time"

	mqttclient "github.com/automatedhome/flow-meter/pkg/mqttclient"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type BoolPoint struct {
	v    bool
	addr string
}

type DataPoint struct {
	v    float64
	addr string
}

type Settings struct {
	tankMin        DataPoint
	tankMax        DataPoint
	heaterCritical DataPoint
	hysteresis     DataPoint
}

type Sensors struct {
	roomTemp  DataPoint
	tankUp    DataPoint
	heaterIn  DataPoint
	heaterOut DataPoint
}

type Actuators struct {
	heater BoolPoint
	sw     BoolPoint //CH == False, DHW == True
}

type ScheduleCell struct {
	From        string  `json:"from"`
	To          string  `json:"to"`
	Temperature float64 `json:"temperature"`
}

type Schedule struct {
	Workday            []ScheduleCell `json:"workday"`
	Freeday            []ScheduleCell `json:"freeday"`
	DefaultTemperature float64        `json:"defaultTemperature"`
}

var settings Settings
var sensors Sensors
var actuators Actuators
var client mqtt.Client

func onMessage(client mqtt.Client, message mqtt.Message) {
	return
}

func heater(state bool, reason string) {
	if state == actuators.heater.v {
		return
	}

	actuators.heater.v = state
	if state {
		log.Println("Starting: " + reason)
		client.Publish(actuators.heater.addr, 0, false, "1")
		return
	}

	if !state {
		log.Println("Stopping: " + reason)
		client.Publish(actuators.heater.addr, 0, false, "0")
	}
}

func sw(destination string) {
	state := false
	if destination == "water" {
		state = true
	}
	if state == actuators.heater.v {
		return
	}

	actuators.sw.v = state
	if state {
		log.Println("Switching actuator in water heating position")
		client.Publish(actuators.sw.addr, 0, false, "1")
		return
	}

	if !state {
		log.Println("Switching actuator in home heating position")
		client.Publish(actuators.sw.addr, 0, false, "0")
	}
}

func isSchedule() (float64, bool) {
	return 18.0, false
}

func init() {
	// TODO read it from yaml file
	settings = Settings{}
	sensors = Sensors{}
	actuators = Actuators{}
	actuators.heater = BoolPoint{false, "heater/actuators/burner"} // proxy to "evok/relay/5/set"
	actuators.sw = BoolPoint{false, "heater/actuators/switch"}     // proxy to "evok/relay/1/set"

	sensors.roomTemp = DataPoint{15.0, "climate/temperature/inside"}
	sensors.tankUp = DataPoint{0, "tank/temperature/up"}
	sensors.heaterIn = DataPoint{0, "heater/temperature/in"}
	sensors.heaterOut = DataPoint{0, "heater/temperature/out"}

	settings.heaterCritical = DataPoint{80, "heater/settings/critical"}
	settings.hysteresis = DataPoint{1, "heater/settings/hysteresis"}
	settings.tankMin = DataPoint{45, "heater/settings/tankmin"}
	settings.tankMax = DataPoint{55, "heater/settings/tankmax"}
}

func main() {
	broker := flag.String("broker", "tcp://127.0.0.1:1883", "The full url of the MQTT server to connect to ex: tcp://127.0.0.1:1883")
	clientID := flag.String("clientid", "heater", "A clientid for the connection")
	flag.Parse()

	brokerURL, _ := url.Parse(*broker)
	var topics []string
	topics = append(topics, sensors.heaterIn.addr, sensors.heaterOut.addr, sensors.tankUp.addr, sensors.roomTemp.addr)
	topics = append(topics, settings.heaterCritical.addr, settings.hysteresis.addr, settings.tankMin.addr, settings.tankMax.addr)
	client = mqttclient.New(*clientID, brokerURL, topics, onMessage)
	log.Printf("Connected to %s as %s and waiting for messages\n", *broker, *clientID)

	// Wait for sensors data
	for {
		if sensors.roomTemp.v != 0 && sensors.tankUp.v != 0 && sensors.heaterIn.v != 0 && sensors.heaterOut.v != 0 {
			break
		}
		log.Println("Waiting 15s for sensors data...")
		time.Sleep(15 * time.Second)
	}
	log.Printf("Starting with sensors data received: %+v\n", sensors)

	// run program
	for {
		time.Sleep(1 * time.Second)

		// failsafe
		if sensors.heaterOut.v >= settings.heaterCritical.v {
			heater(false, "critical heater temperature reached")
			continue
		}

		// check if now is the time to start heating
		expected, isTime := isSchedule()
		if !isTime {
			heater(false, "not a time for heating")
			sw("room")
			continue
		}

		// Water heating start
		if sensors.tankUp.v < settings.tankMin.v {
			heater(true, "water heating")
			sw("water")
			continue
		}

		// water heating ends
		if sensors.tankUp.v >= settings.tankMax.v {
			sw("room")
		}

		// room heating
		if sensors.roomTemp.v < expected-settings.hysteresis.v/2 {
			heater(true, "room temperature lower than expected")
			continue
		}

		if sensors.roomTemp.v > expected+settings.hysteresis.v/2 {
			heater(false, "expected room temperature achieved")
		}

	}
}
