package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"math"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
)

type sensor struct {
	SensorID    string  `json:"sensorid"`
	SensorType  string  `json:"type"`
	SensorValue float64 `json:"value"`
}

var (
	sensors             []sensor
	onewireDevicePath   = "/sys/bus/w1/devices/"
	onewireDeviceList   []string
	hostname, _         = os.Hostname()
	listenAddress       = flag.String("web.listen-address", ":8105", "Address and port to expose metrics")
	metricsPath         = flag.String("web.telemetry-path", "/metrics", "Path under which to expose metrics.")
	jsonMetricsPath     = flag.String("web.json-path", "/json", "Path under which to expose json metrics.")
	enableFahrenheit    = flag.Bool("export.fahrenheit", false, "Include Fahrenheit in export.")
	onewireTemperatureC = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "onewire_temperature_c",
			Help: "Onewire Temperature Sensor Value in Celsius.",
		},
		[]string{
			"device_id",
			"hostname",
		},
	)
	onewireTemperatureF *prometheus.GaugeVec
)

func init() {
	// Log as JSON instead of the default ASCII formatter.
	log.SetFormatter(&log.JSONFormatter{})

	// Output to stdout instead of the default stderr
	// Can be any io.Writer, see below for File example
	log.SetOutput(os.Stdout)

	// Only log the warning severity or above.
	//log.SetLevel(log.WarnLevel)
	// Parsing command line arguments
	flag.Parse()
	// Registers temperature gauges
	prometheus.MustRegister(onewireTemperatureC)
	if *enableFahrenheit {
		onewireTemperatureF = prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "onewire_temperature_f",
				Help: "Onewire Temperature Sensor Value in Fahrenheit.",
			},
			[]string{
				"device_id",
				"hostname",
			},
		)

		prometheus.MustRegister(onewireTemperatureF)
	}
}

func main() {
	log.Info("Started")
	// install net/http handlers
	http.Handle(*metricsPath, promhttp.Handler())
	http.HandleFunc("/", rootPathHandler)
	http.HandleFunc(*jsonMetricsPath, jsonPathHandler)

	// launch prometheus metrics handler as a goroutine
	go observeOnewireTemperature()
	// starts http listener
	log.WithFields(log.Fields{"httpListen": *listenAddress}).Info("Exporter listening")
	log.Fatal(http.ListenAndServe(*listenAddress, nil))

}

func rootPathHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, `<html>
		<head><title>Node Exporter</title></head>
		<body>
		<h1>Node Exporter</h1>
		<p><a href="`+*metricsPath+`">Metrics</a></p>
		<p><a href="`+*jsonMetricsPath+`">JSON Metrics</a></p>
		</body>
		</html>`)
}

func jsonPathHandler(w http.ResponseWriter, r *http.Request) {
	jsonData, _ := json.Marshal(sensors)
	fmt.Fprintf(w, "%s", string(jsonData))
}

func observeOnewireTemperature() {
	// lists onewire devices
	err := createOnewireDeviceList()
	if err != nil {
		log.Fatal("Error getting Onewire device list")
	}
	for {
		sensors = sensors[:len(onewireDeviceList)]
		index := 0
		for _, deviceID := range onewireDeviceList {
			value, err := readOnewireDevicePayload(deviceID)
			if err != nil {
				log.WithFields(log.Fields{"deviceID": deviceID}).Error("Error reading from device")
			}

			fahrenheit := math.Round((value*1.8+32)*100) / 100
			if *enableFahrenheit {
				log.WithFields(log.Fields{"deviceID": deviceID, "value": value, "fahrenheit": fahrenheit, "hostname": hostname}).Info("Value read from device")
			} else {
				log.WithFields(log.Fields{"deviceID": deviceID, "value": value, "hostname": hostname}).Info("Value read from device")
			}

			onewireTemperatureC.With(prometheus.Labels{"device_id": deviceID, "hostname": hostname}).Set(value)
			if *enableFahrenheit {
				onewireTemperatureF.With(prometheus.Labels{"device_id": deviceID, "hostname": hostname}).Set(fahrenheit)
			}

			sensors[index] = sensor{SensorID: deviceID, SensorType: "temperature", SensorValue: value}
			index++
		}
		time.Sleep(60 * time.Second)
	}
}

func readOnewireDevicePayload(deviceID string) (float64, error) {
	devicePayloadFile := fmt.Sprintf("%s%s/w1_slave", onewireDevicePath, deviceID)
	re := regexp.MustCompile(`(?s).*YES.*t=(-?[0-9]+)`)

	for retries := 0; retries < 5; retries++ {
		buffer, err := ioutil.ReadFile(devicePayloadFile)
		if err != nil {
			log.WithFields(log.Fields{"devicePayloadFile": devicePayloadFile}).Error("Error reading Device")
			return 0, err
		}
		match := re.FindStringSubmatch(string(buffer))
		if len(match) > 0 {
			value, _ := strconv.ParseFloat(match[1], 64)
			return value / 1000, nil
		}
		log.WithFields(log.Fields{"deviceID": deviceID, "hostname": hostname}).Warning("Retrying read")
		time.Sleep(1 * time.Second)
	}

	return 0, errors.New("Failed to read device")
}

func createOnewireDeviceList() error {
	devices, err := ioutil.ReadDir(onewireDevicePath)
	if err != nil {
		log.Fatalf("Can't read device directory %v", err)
		return nil
	}
	// searching for onewire attached devices
	for _, device := range devices {
		if strings.Contains(device.Name(), "w1_bus_master1") != true {
			onewireDeviceList = append(onewireDeviceList, device.Name())
			log.Infof("Device found: %s", device.Name())
		}
	}

	sensors = make([]sensor, len(onewireDeviceList))

	return nil
}
