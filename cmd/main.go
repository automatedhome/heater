package main

import (
	"flag"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/yaml.v2"

	mqttclient "github.com/automatedhome/common/pkg/mqttclient"
	types "github.com/automatedhome/heater/pkg/types"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

const (
	ROOM  = false
	WATER = true
)

var (
	config      types.Config
	settings    types.Settings
	sensors     types.Sensors
	actuators   types.Actuators
	client      mqtt.Client
	heaterState bool
	switchState bool
)

var (
	failsafeTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "heater_failsafe_total",
		Help: "Increase when failsafe system kicked in",
	})
	actuatorMode = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "heater_actuator_mode_binary",
		Help: "Desired state for heater actuator",
	})
	burnerMode = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "heater_burner_mode_binary",
		Help: "Desired state for heater burner",
	})
)

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

func heater(desiredState bool, reason string) {
	var msg string
	if desiredState == heaterState {
		return
	}

	if desiredState {
		msg = "1"
		log.Println("Starting: " + reason)
		burnerMode.Set(1)
	} else {
		msg = "0"
		log.Println("Stopping: " + reason)
		burnerMode.Set(0)
	}

	if err := mqttclient.Publish(client, actuators.Heater, 0, false, msg); err != nil {
		return
	}

	heaterState = desiredState
}

func sw(desiredState bool) {
	var msg string
	if switchState == desiredState {
		return
	}

	if desiredState {
		msg = "1"
		log.Println("Switching actuator in water heating position")
		actuatorMode.Set(1)
	} else {
		msg = "0"
		log.Println("Switching actuator in home heating position")
		actuatorMode.Set(0)
	}
	if err := mqttclient.Publish(client, actuators.Switch, 0, false, msg); err != nil {
		return
	}

	switchState = desiredState
}

func failsafe() bool {
	if sensors.HeaterOut.Value >= settings.HeaterCritical.Value {
		heater(false, "critical heater temperature reached")
		failsafeTotal.Inc()
		time.Sleep(1 * time.Second)
		return true
	}
	return false
}

// waterHeatingController returns true when water heating is ON
func waterHeatingController(temperature float64, min float64, max float64) bool {
	// Water heating start
	if temperature < min {
		heater(true, "water heating")
		time.Sleep(1 * time.Second)
		sw(WATER)
		time.Sleep(1 * time.Second)
		return true
	}

	// water heating ends
	if temperature >= max {
		sw(ROOM)
		time.Sleep(1 * time.Second)
		return false
	}

	return switchState
}

func roomHeatingController(temperature float64, expected float64, hysteresis float64) {
	// room heating
	if temperature < expected-hysteresis {
		heater(true, "room temperature lower than expected")
		time.Sleep(1 * time.Second)
		return
	}

	if temperature > expected+hysteresis {
		heater(false, "expected room temperature achieved")
		time.Sleep(1 * time.Second)
	}
}

func init() {
	heaterState = false
	switchState = false

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

	if err := yaml.UnmarshalStrict(data, &config); err != nil {
		log.Fatalf("error: %v", err)
	}
	log.Printf("Reading following config from config file: %#v", config)

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
	if err := mqttclient.Publish(client, actuators.Heater, 0, false, "0"); err != nil {
		log.Fatalf("Cannot reset heater. Exiting.")
	}
	if err := mqttclient.Publish(client, actuators.Switch, 0, false, "0"); err != nil {
		log.Fatalf("Cannot reset switch. Exiting.")
	}

	// Register metrics endpoint
	http.Handle("/metrics", promhttp.Handler())
	http.ListenAndServe(":7002", nil)

	// Wait for sensors data
	waitForData(lockTemp)
}

func main() {
	for {
		time.Sleep(1 * time.Second)

		if failsafe() {
			continue
		}

		if waterHeatingController(sensors.TankUp.Value, settings.TankMin.Value, settings.TankMax.Value) {
			continue
		}

		roomHeatingController(sensors.RoomTemp.Value, settings.Expected.Value, settings.Hysteresis.Value)
	}
}
