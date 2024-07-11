package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
)

var gacek_home = "/mnt/nfs/gacek"
var jobs_dir = gacek_home + "/jobs"
var queues_dir = gacek_home + "/queues"

func get_pending_jobs() []string {
	dir, err := os.Open(queues_dir + "/pending")
	if err != nil {
		log.Fatal(err)
	}
	defer dir.Close()
	jobs, err := dir.Readdirnames(0)
	if err != nil {
		log.Fatal(err)
	}
	return jobs
}

func move_job(job string, dest string) {
	log.Printf("Marking job %s as %s\n", job, dest)
	pending_path := queues_dir + "/pending/" + job
	dest_path := queues_dir + "/" + dest + "/" + job
	err := os.Rename(pending_path, dest_path)
	if err != nil {
		log.Fatal(err)
	}
}

func fetch_tf_status(id string) (string, string) {
	log.Printf("Fetching status of TF request ID %s\n", id)
	url := "https://api.testing-farm.io/v0.1/requests/" + id
	resp, err := http.Get(url)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Fatal(fmt.Errorf("HTTP GET failed: %s", resp.Status))
	}
	bytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err)
	}
	tf := struct {
		State string `json:"state"`
		Run   struct {
			Artifacts string `json:"artifacts"`
		} `json:"run"`
		Result struct {
			Overall string `json:"overall"`
		} `json:"result"`
	}{}
	if err := json.Unmarshal(bytes, &tf); err != nil {
		log.Fatal(err)
	}
	log.Printf("TF state is %s\n", tf.State)
	log.Printf("TF artifacts URL: %s\n", tf.Run.Artifacts)
	// TF request statuses: new, queued, running, error, complete, cancel-requested, canceled
	// From https://gitlab.com/testing-farm/nucleus.git
	// File api/src/tft/nucleus/api/core/schemes/test_request.py
	if tf.State != "error" && tf.State != "complete" && tf.State != "canceled" {
		// Final statuses are: error, complete, canceled
		// Treat all non-final states as equivalent to "pending"
		return "pending", ""
	}
	if tf.State != "complete" {
		return "error", ""
	}
	if tf.Result.Overall != "passed" && tf.Result.Overall != "failed" {
		return "error", ""
	}
	return "complete", tf.Run.Artifacts
}

func fetch_artifact(url string, path string) {
	log.Printf("Fetching TF artifact %s to %s\n", url, path)
	resp, err := http.Get(url)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("HTTP status code: %d\n", resp.StatusCode)
		log.Fatal("Artifact fetch failed")
	}
	out, err := os.Create(path)
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		log.Fatal(err)
	}
}

func get_tf_id(job string) string {
	job_dir := jobs_dir + "/" + job
	bytes, err := os.ReadFile(job_dir + "/tf-dispatch.xml")
	if err != nil {
		log.Fatal(err)
	}
	dispatch := struct {
		TfId string `xml:"tfId"`
	}{}
	if err := xml.Unmarshal(bytes, &dispatch); err != nil {
		log.Fatal(err)
	}
	log.Printf("TF request ID is %s\n", dispatch.TfId)
	return dispatch.TfId
}

func poll_job(job string) {
	log.Printf("Polling job %s\n", job)
	tf_id := get_tf_id(job)
	status, artifacts := fetch_tf_status(tf_id)
	log.Printf("TF status is %s\n", status)
	if status == "error" {
		move_job(job, "error")
	} else if status == "complete" {
		job_dir := jobs_dir + "/" + job
		results_path := job_dir + "/results.xml"
		fetch_artifact(artifacts+"/results.xml", results_path)
		bytes, err := os.ReadFile(results_path)
		if err != nil {
			log.Fatal(err)
		}
		results := struct {
			Plans []struct {
				Name string `xml:"name,attr"`
				Logs []struct {
					Name string `xml:"name,attr"`
					Url  string `xml:"href,attr"`
				} `xml:"logs>log"`
			} `xml:"testsuite"`
		}{}
		if err := xml.Unmarshal(bytes, &results); err != nil {
			log.Fatal(err)
		}
		for _, plan := range results.Plans {
			log.Printf("Found tmt plan %s\n", plan.Name)
			workdir := ""
			for _, log := range plan.Logs {
				if log.Name == "workdir" {
					workdir = log.Url
				}
			}
			if workdir != "" {
				log.Printf("Plan workdir: %s\n", workdir)
				fetch_artifact(workdir+"/"+plan.Name+"/discover/tests.yaml", job_dir+plan.Name+"-tests.yaml")
			}
		}
		move_job(job, "complete")
	}
}

func main() {
	log.Printf("Polling started\n")
	for _, job := range get_pending_jobs() {
		poll_job(job)
	}
	log.Printf("Polling complete\n")
}
