package main

import (
	"flag"
	"io/ioutil"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v2"

	mqttclient "github.com/automatedhome/common/pkg/mqttclient"
	types "github.com/automatedhome/heater/pkg/types"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

var config types.Config
var settings types.Settings
var sensors types.Sensors
var actuators types.Actuators
var client mqtt.Client
var heaterState bool
var switchState bool

func onMessage(client mqtt.Client, message mqtt.Message) {
	value, err := strconv.ParseFloat(string(message.Payload()), 64)
	if err != nil {
		return
	}

	switch message.Topic() {
	case sensors.RoomTemp.Address:
		sensors.RoomTemp.Value = value
	case sensors.TankUp.Address:
		sensors.TankUp.Value = value
	case sensors.HeaterIn.Address:
		sensors.HeaterIn.Value = value
	case sensors.HeaterOut.Address:
		sensors.HeaterOut.Value = value
	case settings.TankMin.Address:
		settings.TankMin.Value = value
	case settings.TankMax.Address:
		settings.TankMax.Value = value
	case settings.HeaterCritical.Address:
		settings.HeaterCritical.Value = value
	case settings.Expected.Address:
		settings.Expected.Value = value
	case settings.Hysteresis.Address:
		settings.Hysteresis.Value = value
	}
}

func waitForData(lockValue float64) {
	// Wait for sensors data
	for {
		if sensors.RoomTemp.Value != lockValue && sensors.TankUp.Value != lockValue && sensors.HeaterIn.Value != lockValue && sensors.HeaterOut.Value != lockValue {
			break
		}
		msg := []string{"Waiting 15s for sensors data. Currently lacking:"}
		if sensors.HeaterIn.Value == 300 {
			msg = append(msg, "heaterIn")
		}
		if sensors.HeaterOut.Value == 300 {
			msg = append(msg, "heaterOut")
		}
		if sensors.RoomTemp.Value == 300 {
			msg = append(msg, "roomTemp")
		}
		if sensors.TankUp.Value == 300 {
			msg = append(msg, "tankUp")
		}
		log.Println(strings.Join(msg, " "))
		time.Sleep(15 * time.Second)
	}
	log.Printf("Starting with sensors data received: %+v\n", sensors)
}

func heater(state bool, reason string) {
	if state == heaterState {
		return
	}

	heaterState = state
	if state {
		log.Println("Starting: " + reason)
		client.Publish(actuators.Heater, 0, false, "1")
		return
	}

	log.Println("Stopping: " + reason)
	client.Publish(actuators.Heater, 0, false, "0")
}

func sw(destination string) {
	state := false
	if destination == "water" {
		state = true
	}

	if switchState == state {
		return
	}

	if state {
		log.Println("Switching actuator in water heating position")
		client.Publish(actuators.Switch, 0, false, "1")
		switchState = true
		return
	}

	log.Println("Switching actuator in home heating position")
	client.Publish(actuators.Switch, 0, false, "0")
	switchState = false
}

func failsafe() bool {
	if sensors.HeaterOut.Value >= settings.HeaterCritical.Value {
		heater(false, "critical heater temperature reached")
		time.Sleep(1 * time.Second)
		return true
	}
	return false
}

// water returns true when water heating is ON
func water(value float64, min float64, max float64) bool {
	// Water heating start
	if value < min {
		heater(true, "water heating")
		time.Sleep(1 * time.Second)
		sw("water")
		time.Sleep(1 * time.Second)
		return true
	}

	// water heating ends
	if value >= max {
		sw("room")
		time.Sleep(1 * time.Second)
		return false
	}

	return switchState
}

func init() {
	heaterState = false
	switchState = false
}

func main() {
	broker := flag.String("broker", "tcp://127.0.0.1:1883", "The full url of the MQTT server to connect to ex: tcp://127.0.0.1:1883")
	clientID := flag.String("clientid", "heater", "A clientid for the connection")
	configFile := flag.String("config", "/config.yaml", "Provide configuration file with MQTT topic mappings")
	flag.Parse()

	brokerURL, err := url.Parse(*broker)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Reading configuration from %s", *configFile)
	data, err := ioutil.ReadFile(*configFile)
	if err != nil {
		log.Fatalf("File reading error: %v", err)
		return
	}

	err = yaml.UnmarshalStrict(data, &config)
	//err = yaml.Unmarshal(data, &config)
	if err != nil {
		log.Fatalf("error: %v", err)
	}

	settings = config.Settings
	actuators = config.Actuators
	sensors = config.Sensors
	// set initial sensors values and ignore ones provided by config file
	// this is used as a locking mechanism to prevent starting control loop without current sensors data
	lockTemp := 300.0
	sensors.HeaterIn.Value = lockTemp
	sensors.HeaterOut.Value = lockTemp
	sensors.RoomTemp.Value = lockTemp
	sensors.TankUp.Value = lockTemp

	var topics []string
	topics = append(topics, sensors.HeaterIn.Address, sensors.HeaterOut.Address, sensors.TankUp.Address, sensors.RoomTemp.Address)
	topics = append(topics, settings.HeaterCritical.Address, settings.Hysteresis.Address, settings.TankMin.Address, settings.TankMax.Address, settings.Expected.Address)
	client = mqttclient.New(*clientID, brokerURL, topics, onMessage)
	log.Printf("Connected to %s as %s and waiting for messages\n", *broker, *clientID)

	// Reseting state to OFF
	client.Publish(actuators.Heater, 0, false, "0")
	time.Sleep(1 * time.Second)
	client.Publish(actuators.Switch, 0, false, "0")

	// Wait for sensors data
	waitForData(lockTemp)

	// Step 2. - RUN forever
	for {
		time.Sleep(1 * time.Second)

		if failsafe() {
			continue
		}

		if water(sensors.TankUp.Value, settings.TankMin.Value, settings.TankMax.Value) {
			continue
		}

		// room heating
		if sensors.RoomTemp.Value < settings.Expected.Value-settings.Hysteresis.Value/2 {
			heater(true, "room temperature lower than expected")
			time.Sleep(1 * time.Second)
			continue
		}

		if sensors.RoomTemp.Value > settings.Expected.Value+settings.Hysteresis.Value/2 {
			heater(false, "expected room temperature achieved")
			time.Sleep(1 * time.Second)
		}
	}
}
