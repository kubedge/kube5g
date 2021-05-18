/*
#!/usr/local/go/bin/go
################################################################################
* Copyright 2016-2019 Eurecom and Mosaic5G Platforms Authors
* Licensed to the Mosaic5G under one or more contributor license
* agreements. See the NOTICE file distributed with this
* work for additional information regarding copyright ownership.
* The Mosaic5G licenses this file to You under the
* Apache License, Version 2.0  (the "License");
* you may not use this file except in compliance with the License.
* You may obtain a copy of the License at
*
*      http://www.apache.org/licenses/LICENSE-2.0
*
* Unless required by applicable law or agreed to in writing, software
* distributed under the License is distributed on an "AS IS" BASIS,
* WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
* See the License for the specific language governing permissions and
* limitations under the License.
################################################################################
#-------------------------------------------------------------------------------
# For more information about Mosaic5G:
#                                   admin@mosaic-5g.io
# file          enb.go
# brief 		configure the snap of oai-ran, and start it
# authors:
	- Osama Arouk (arouk@eurecom.fr)
	- Kevin Hsi-Ping Hsu (hsuh@eurecom.fr)
*-------------------------------------------------------------------------------
*/
package oai

import (
	"errors"
	"log"
	"mosaic5g/docker-hook/internal/pkg/util"
	"net"
	"strings"
	"time"
)

func replaceTextInFile(fieldName string, replacingValue string, OaiObj Oai, ueConf string) error {
	sedCommand := "s/" + fieldName + " \\+=.[^;]*/" + fieldName + " = " + replacingValue + "/g"
	OaiObj.Logger.Print("Replace " + fieldName)
	OaiObj.Logger.Print(sedCommand)
	retStatus := util.RunCmd(OaiObj.Logger, "sed", "-i", sedCommand, ueConf)
	if retStatus.Exit != 0 {
		return errors.New("Set " + fieldName + " in " + ueConf + " failed")
	}
	return nil
}

type LineAndValue struct {
	LineNumber string
	Value      string
}

type NetInterface struct {
	Name string
	IP   string
}

func getListOfInterface(logger *log.Logger) []NetInterface {
	ret := util.RunCmd(logger, "bash", "-c", "ip -4 -o a | cut -d ' ' -f 2,7 | cut -d '/' -f 1")
	oout := ret.Stdout
	var res = make([]NetInterface, len(oout))
	next_slot := 0
	for _, line := range oout {
		items := strings.Fields(line)
		if len(items) >= 2 {
			res[next_slot] = NetInterface{
				Name: items[0],
				IP:   items[1],
			}
			next_slot++
		}
	}
	// fmt.Printf("%v\n", res)
	return res
}

//TODO: fuse this function with above function to reduce the code
func replaceIpAddr(fieldName string, lineNumber string, replacingValue string, OaiObj Oai, ueConf string) error {
	sedCommand := lineNumber + "s:\".*;:\"" + replacingValue + "\";:g"
	OaiObj.Logger.Print("Replace " + fieldName)
	OaiObj.Logger.Print(sedCommand)
	retStatus := util.RunCmd(OaiObj.Logger, "sed", "-i", sedCommand, ueConf)
	if retStatus.Exit != 0 {
		return errors.New("Set " + fieldName + " in " + ueConf + " failed")
	}
	return nil
}

func startUESim(OaiObj Oai, buildSnap bool) error {
	c := OaiObj.Conf
	cmdNodeFunction := "oai-sim.ue"

	OaiObj.Logger.Print("getting the config file of ue-sim")
	retStatus := util.RunCmd(OaiObj.Logger, cmdNodeFunction+"-conf-get")
	confFileName := ""
	if retStatus.Exit == 0 {
		s := strings.Split(retStatus.Stdout[0], "/")
		confFileName = s[len(s)-1]
		OaiObj.Logger.Print("the config file of ue-sim is " + confFileName)
	} else {
		var outError string
		for i := 0; i < len(retStatus.Stderr); i++ {
			outError += retStatus.Stderr[i] + "\n"
		}
		return errors.New("Error while getting the config file of ue-sim \n" + outError)
	}

	uesimConfLinesStr := util.RunCmd(OaiObj.Logger, cmdNodeFunction+"-conf-show")
	var uesimConfStr string
	for i := 0; i < len(uesimConfLinesStr.Stdout); i++ {
		uesimConfStr += uesimConfLinesStr.Stdout[i] + "\n"
	}

	retStatus = util.RunCmd(OaiObj.Logger, "which", cmdNodeFunction+"-status")
	s := strings.Split(retStatus.Stdout[0], "/")
	snapBinaryPath := strings.Join(s[0:len(s)-1], "/")

	// Stop oai-ue-sim
	OaiObj.Logger.Print("Stop ue-sim daemon")
	for {
		retStatus := util.RunCmd(OaiObj.Logger, strings.Join([]string{snapBinaryPath, cmdNodeFunction + "-stop"}, "/"))

		if len(retStatus.Stderr) == 0 {
			oaiRanDisabledInactive := 0
			for {
				time.Sleep(1 * time.Second)
				retStatus = util.RunCmd(OaiObj.Logger, strings.Join([]string{snapBinaryPath, cmdNodeFunction + "-status"}, "/"))
				oairanStatus := strings.Join(retStatus.Stdout, " ")
				if strings.Contains(oairanStatus, "disabled") && strings.Contains(oairanStatus, "inactive") {
					oaiRanDisabledInactive = 1
					break
				}
			}
			if oaiRanDisabledInactive == 1 {
				break
			}
		}
		// adapt the name of package
		OaiObj.Logger.Print("Stop " + cmdNodeFunction + " failed, try again later")
		time.Sleep(1 * time.Second)
	}

	retStatus = util.RunCmd(OaiObj.Logger, strings.Join([]string{snapBinaryPath, cmdNodeFunction + "-conf-get"}, "/"))

	OaiObj.Logger.Print("confFileName=" + confFileName)
	s = strings.Split(retStatus.Stdout[0], "/")
	ueConf := strings.Join(s[0:len(s)-1], "/")
	ueConf = strings.Join([]string{ueConf, confFileName}, "/")
	OaiObj.Logger.Print("confFileName=" + confFileName)
	OaiObj.Logger.Print("ueConf=", ueConf)
	retStatus = util.RunCmd(OaiObj.Logger, strings.Join([]string{snapBinaryPath, cmdNodeFunction + "-conf-set"}, "/"), ueConf)

	OaiObj.Logger.Print("Start to replace the key")

	// get the name of this interface
	listInterface := getListOfInterface(OaiObj.Logger)
	// assume that docker will create the default interface that is "eth0"
	defaultInterface := "eth0"
	found := false
	currentIP := ""
	currentInterface := ""
	for _, namedInterface := range listInterface {
		if namedInterface.Name == defaultInterface {
			found = true
			currentIP = namedInterface.IP
			currentInterface = namedInterface.Name
		}
	}
	if !found {
		return errors.New("don't found the interface for uesim container")
	}

	// a map of supported key
	supportedParameter := map[string]string{
		"max_rxgain":      c.OaiUesim[0].RUs[0].MaxRxGain.Default,
		"bands":           c.OaiUesim[0].RUs[0].Bands.Default,
		"num_cc":          c.OaiUesim[0].L1s[0].NumCC.Default,
		"tr_n_preference": c.OaiUesim[0].L1s[0].TrNPreference.Default,
		// "local_n_portc":   c.OaiUesim[0].L1s[0].LocalNPortc.Default,
		"remote_n_portc": c.OaiUesim[0].L1s[0].RemoteNPortc.Default,
		// "local_n_portd":   c.OaiUesim[0].L1s[0].LocalNPortd.Default,
		"remote_n_portd":  c.OaiUesim[0].L1s[0].RemoteNPortd.Default,
		"local_n_if_name": currentInterface,
	}

	for fieldName, valueReplacing := range supportedParameter {
		ret := replaceTextInFile(fieldName, valueReplacing, OaiObj, ueConf)
		if ret != nil {
			// error occurs
			return ret
		}
	}

	if !buildSnap {
		OaiObj.Logger.Print("Start to replace the the address")

		// get input from user and determine if it is host or ip
		stringIsHost := true
		remoteIP := ""
		addr := net.ParseIP(c.OaiUesim[0].L1s[0].RemoteNAddress.Default)
		if addr != nil {
			stringIsHost = false
			remoteIP = c.OaiUesim[0].L1s[0].RemoteNAddress.Default
		}

		if stringIsHost {
			n := 0
			max_attempt := 100
			foundIP := false
			for n < max_attempt {
				tempRemoteIP, err := util.GetIPFromDomain(OaiObj.Logger,
					c.OaiUesim[0].L1s[0].RemoteNAddress.Default)
				OaiObj.Logger.Print("err = ", err)
				OaiObj.Logger.Print("enbIP = ", tempRemoteIP)
				if err != nil {
					OaiObj.Logger.Print(err)
				} else {
					foundIP = true
					remoteIP = tempRemoteIP
					break
				}
				OaiObj.Logger.Print("Valid ip address for Enb not get retreived")
				time.Sleep(1 * time.Second)
			}
			if !foundIP {
				return errors.New("cannot resolve the ip of enb from conf.yaml")
			}
		}

		// replace the address
		ipAddrParameter := map[string]LineAndValue{
			"remote_n_address": {
				Value:      remoteIP,
				LineNumber: "28",
			},
			"local_n_address": {
				Value:      currentIP,
				LineNumber: "29",
			},
		}

		for fieldName, valueReplacing := range ipAddrParameter {
			ret := replaceIpAddr(fieldName, valueReplacing.LineNumber,
				valueReplacing.Value, OaiObj, ueConf)
			if ret != nil {
				// error occurs
				return ret
			}
		}
	}

	// start ue-sim

	OaiObj.Logger.Print("Enable IP v6")
	retStatus = util.RunCmd(OaiObj.Logger, "sysctl", "-w", "net.ipv6.conf.all.disable_ipv6=0")
	retStatus = util.RunCmd(OaiObj.Logger, "sysctl", "-w", "net.ipv6.conf.default.disable_ipv6=0")

	retStatus = util.RunCmd(OaiObj.Logger, strings.Join([]string{snapBinaryPath, cmdNodeFunction + "-start"}, "/"))
	time.Sleep(5 * time.Second)
	var counterOairabActiveTime int64
	var counter int64

	counter = 0
	counterOairabActiveTime = 30
	for {
		time.Sleep(1 * time.Second)
		if len(retStatus.Stderr) == 0 {
			counter++
			retStatus = util.RunCmd(OaiObj.Logger, strings.Join([]string{snapBinaryPath, cmdNodeFunction + "-status"}, "/"))
			oaiUeStatus := strings.Join(retStatus.Stdout, " ")
			checkInactive := strings.Contains(oaiUeStatus, "inactive")
			if !checkInactive {
				if counter >= counterOairabActiveTime {
					OaiObj.Logger.Print("ue-sim is working, exit...")
					break
				}
			} else {
				OaiObj.Logger.Print("ue-sim is in inactive status, restarting the service")
				util.RunCmd(OaiObj.Logger, strings.Join([]string{snapBinaryPath, cmdNodeFunction + "-stop"}, "/"))
				for {
					time.Sleep(1 * time.Second)
					retStatus = util.RunCmd(OaiObj.Logger, strings.Join([]string{snapBinaryPath, cmdNodeFunction + "-status"}, "/"))
					oairanStatus := strings.Join(retStatus.Stdout, " ")
					if strings.Contains(oairanStatus, "disabled") && strings.Contains(oairanStatus, "inactive") {
						break
					}
				}
				// time.Sleep(5 * time.Second)
				retStatus = util.RunCmd(OaiObj.Logger, strings.Join([]string{snapBinaryPath, cmdNodeFunction + "-start"}, "/"))
				time.Sleep(5 * time.Second)
				counter = 0
			}
		} else {
			OaiObj.Logger.Print("Start ue failed, try again later")
			retStatus = util.RunCmd(OaiObj.Logger, strings.Join([]string{snapBinaryPath, cmdNodeFunction + "-start"}, "/"))
			time.Sleep(5 * time.Second)
			counter = 0
		}
	}

	OaiObj.Logger.Print("ue daemon Started")

	return nil
}
