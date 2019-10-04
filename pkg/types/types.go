package types

import (
	common "github.com/automatedhome/common/pkg/types"
)

type Settings struct {
	TankMin        common.DataPoint `yaml:"tankMin"`
	TankMax        common.DataPoint `yaml:"tankMax"`
	HeaterCritical common.DataPoint `yaml:"heaterCritical"`
	Hysteresis     common.DataPoint `yaml:"hysteresis"`
	Expected       common.DataPoint `yaml:"expected"`
}

type Sensors struct {
	HeaterIn  common.DataPoint `yaml:"heaterIn"`
	HeaterOut common.DataPoint `yaml:"heaterOut"`
	RoomTemp  common.DataPoint `yaml:"roomTemp"`
	TankUp    common.DataPoint `yaml:"tankUp"`
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
