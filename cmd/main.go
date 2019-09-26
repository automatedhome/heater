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
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type DataPoint struct {
	Value   float64 `yaml:"value,omitempty"`
	Address string  `yaml:"address"`
}

type Settings struct {
	TankMin        DataPoint `yaml:"tankMin"`
	TankMax        DataPoint `yaml:"tankMax"`
	HeaterCritical DataPoint `yaml:"heaterCritical"`
	Hysteresis     DataPoint `yaml:"hysteresis"`
	Expected       DataPoint `yaml:"expected"`
}

type Sensors struct {
	HeaterIn  DataPoint `yaml:"heaterIn"`
	HeaterOut DataPoint `yaml:"heaterOut"`
	RoomTemp  DataPoint `yaml:"roomTemp"`
	TankUp    DataPoint `yaml:"tankUp"`
}

type Actuators struct {
	Heater string `yaml:"heater"`
	Switch string `yaml:"switch"` //CH == False, DHW == True
}

type Config struct {
	Actuators Actuators `yaml:"actuators"`
	Sensors   Sensors   `yaml:"sensors"`
	Settings  Settings  `yaml:"settings"`
}

var config Config
var settings Settings
var sensors Sensors
var actuators Actuators
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
	if state == heaterState {
		return
	}

	switchState = state
	if state {
		log.Println("Switching actuator in water heating position")
		client.Publish(actuators.Switch, 0, false, "1")
		return
	}

	log.Println("Switching actuator in home heating position")
	client.Publish(actuators.Switch, 0, false, "0")
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

	// Wait for sensors data
	for {
		if sensors.RoomTemp.Value != lockTemp && sensors.TankUp.Value != lockTemp && sensors.HeaterIn.Value != lockTemp && sensors.HeaterOut.Value != lockTemp {
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

	// Step 2. - RUN forever
	for {
		time.Sleep(1 * time.Second)

		// failsafe
		if sensors.HeaterOut.Value >= settings.HeaterCritical.Value {
			heater(false, "critical heater temperature reached")
			continue
		}

		// Water heating start
		if sensors.TankUp.Value < settings.TankMin.Value {
			heater(true, "water heating")
			sw("water")
			continue
		}

		// water heating ends
		if sensors.TankUp.Value >= settings.TankMax.Value {
			sw("room")
		}

		// room heating
		if sensors.RoomTemp.Value < settings.Expected.Value-settings.Hysteresis.Value/2 {
			heater(true, "room temperature lower than expected")
			continue
		}

		if sensors.RoomTemp.Value > settings.Expected.Value+settings.Hysteresis.Value/2 {
			heater(false, "expected room temperature achieved")
		}
	}
}
