/*
 * Copyright 2018 InfAI (CC SES)
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *    http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"encoding/json"
	"github.com/SmartEnergyPlatform/amqp-wrapper-lib"
	"log"
)

type AmqpInterface interface {
	Consume(qname string, resource string, worker amqp_wrapper_lib.ConsumerFunc) (err error)
	Publish(resource string, payload []byte) error
	Close()
}

var amqp AmqpInterface

type AbstractProcess struct {
	Xml                     string      `json:"xml"`
	Name                    string      `json:"name"`
	AbstractTasks           interface{} `json:"abstract_tasks"`
	AbstractDataExportTasks interface{} `json:"abstract_data_export_tasks"`
	ReceiveTasks            interface{} `json:"receive_tasks"`
	MsgEvents               interface{} `json:"msg_events"`
	TimeEvents              interface{} `json:"time_events"`
}

type DeploymentRequest struct {
	Svg     string          `json:"svg"`
	Process AbstractProcess `json:"process"`
}

type DeploymentCommand struct {
	Command       string            `json:"command"`
	Id            string            `json:"id"`
	Owner         string            `json:"owner"`
	DeploymentXml string            `json:"deployment_xml"`
	Deployment    DeploymentRequest `json:"deployment"`
}

func InitEventSourcing() (err error) {
	amqp, err = amqp_wrapper_lib.Init(Config.AmqpUrl, []string{Config.AmqpDeploymentTopic}, Config.AmqpReconnectTimeout)
	if err != nil {
		return err
	}
	err = amqp.Consume(Config.AmqpConsumerName+"_"+Config.AmqpDeploymentTopic, Config.AmqpDeploymentTopic, func(delivery []byte) error {
		maintenanceLock.RLock()
		defer maintenanceLock.RUnlock()
		command := DeploymentCommand{}
		err = json.Unmarshal(delivery, &command)
		if err != nil {
			log.Println("ERROR: unable to parse amqp event as json \n", err, "\n --> ignore event \n", string(delivery))
			return nil
		}
		log.Println("amqp receive ", string(delivery))
		switch command.Command {
		case "PUT":
			return handleDeploymentCreate(command)
		case "POST":
			log.Println("WARNING: deprecated event type POST")
			return nil
		case "DELETE":
			return handleDeploymentDelete(command.Id)
		default:
			log.Println("WARNING: unknown event type", string(delivery))
			return nil
		}
	})
	return err
}

func handleDeploymentDelete(vid string) error {
	id, exists, err := getDeploymentId(vid)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	commit, rollback, err := removeVidRelation(vid, id)
	if err != nil {
		return err
	}
	err = RemoveProcess(id)
	if err != nil {
		rollback()
	} else {
		commit()
	}
	return err
}

func handleDeploymentCreate(command DeploymentCommand) (err error) {
	err = cleanupExistingDeployment(command.Id)
	if err != nil {
		return err
	}
	deploymentId, err := DeployProcess(command.Deployment.Process.Name, command.DeploymentXml, command.Deployment.Svg, command.Owner)
	if err != nil {
		log.Println("WARNING: unable to deploy process to camunda ", err)
		return err
	}
	err = saveVidRelation(command.Id, deploymentId)
	if err != nil {
		log.Println("WARNING: unable to publish deployment saga \n", err, "\nremove deployed process")
		removeErr := RemoveProcess(deploymentId)
		if removeErr != nil {
			log.Println("ERROR: unable to remove deployed process", deploymentId, err)
		}
		return err
	}
	return err
}

func cleanupExistingDeployment(vid string) error {
	exists, err := vidExists(vid)
	if err != nil {
		return err
	}
	if exists {
		return handleDeploymentDelete(vid)
	}
	return nil
}

func CloseEventSourcing() {
	amqp.Close()
}

func PublishDeploymentDelete(id string) error {
	command := DeploymentCommand{Id: id, Command: "DELETE"}
	payload, err := json.Marshal(command)
	if err != nil {
		return err
	}
	return amqp.Publish(Config.AmqpDeploymentTopic, payload)
}
