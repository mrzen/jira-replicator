package client

import (
	"net/url"
	"net/http"
	"log"
	"os"
	"io/ioutil"
	"encoding/json"
	"errors"
	"bytes"

	"io"
	"time"
	"strconv"
)

// Client represents a JIRA backup client
type Client struct {
	BaseURL url.URL
	HTTP    *http.Client
	Log     *log.Logger

	username string
	password string
}

type BackupStatus struct {
	Status string `json:"status"`
	Progress int `json:"progress"`
	DownloadPath string `json:"result"`
}


// New creates a new JIRA Backup Client
func New(baseURL *url.URL, username, password string) *Client {

	return &Client{
		BaseURL: *baseURL,
		HTTP: &http.Client{Transport: http.DefaultTransport},
		Log: log.New(os.Stdout, "jira-client ", log.LstdFlags),

		username: username,
		password: password,
	}
}

func (c Client) makeRequest(req *http.Request) (*http.Response, error) {

	if req.Header == nil {
		req.Header = http.Header{}
	}

	req.Header.Set("User-Agent", "Jira Replicator +https://bitbucket.org/mr-zen/jira-replicator")
	req.SetBasicAuth(c.username, c.password)

	st := time.Now()
	res, err := c.HTTP.Do(req)
	dt := time.Now().Sub(st)

	if err == nil {
		c.Log.Println(req.Method, req.URL, res.StatusCode, res.Header.Get("Content-Length"), dt)
	}

	return res, err
}

// CreateBackup creates a new JIRA backup.
func (c Client) CreateBackup(includeAttachments bool) error {

	u := c.BaseURL
	u.Path = "/rest/backup/1/export/runbackup"

	req := &http.Request{
		Method: http.MethodPost,
		URL: &u,
		Header: http.Header{
			"Accept": []string{"application/json"},
			"Content-Type": []string{"application/json"},
		},
	}

	params := make(map[string]bool)
	params["cbAttachments"] = includeAttachments
	body, _ := json.Marshal(params)

	req.Body = ioutil.NopCloser(bytes.NewReader(body))

	res, err := c.makeRequest(req)

	if err != nil {
		return err
	}

	if res.StatusCode >= 400 {
		if res.StatusCode == http.StatusPreconditionFailed /* 412 */ {
			// We got us some json, let's get some deets.
			body, err := ioutil.ReadAll(res.Body)

			if err != nil {
				return err
			}

			respData := make(map[string]string)

			err = json.Unmarshal(body, &respData)

			if err != nil {
				return err
			}

			return BackupRateExceeded{}.FromResponse(respData["error"])
		}

		content, _ := ioutil.ReadAll(res.Body)

		return errors.New(string(content))
	}



	return err
}

// Download backup downloads the current JIRA backup
func (c Client) DownloadBackup() (io.ReadCloser, int, error) {

	// Check the backup is ready and waiting.
	status, err := c.GetBackupStatus()

	if err != nil {
		return nil, 0, err
	}

	if status.Status != "Success" {
		return nil, 0, errors.New(status.Status)
	}

	downloadURL, err := url.Parse(status.DownloadPath)

	u := c.BaseURL
	u.Path = "/plugins/servlet/"+downloadURL.Path
	u.RawQuery = downloadURL.Query().Encode()

	req := &http.Request{
		Method: http.MethodGet,
		URL: &u,
	}

	res, err := c.makeRequest(req)

	if err != nil {
		return nil, 0, err
	}

	length, _ := strconv.ParseInt(res.Header.Get("Content-Length"), 10, 64)


	return res.Body, int(length), nil
}

func (c Client) GetBackupStatus() (BackupStatus, error) {

	u := c.BaseURL
	u.Path = "/rest/backup/1/export/lastTaskId"
	q := u.Query()
	q.Set("_", strconv.Itoa(int(time.Now().Unix())))
	u.RawQuery = q.Encode()

	// First determine the task ID.
	req := &http.Request{
		Method: http.MethodGet,
		URL: &u,
		Header: http.Header{
			"Accept": []string{"application/json"},
		},
	}

	res, err := c.makeRequest(req)

	if err != nil {
		return BackupStatus{}, err
	}

	if res.StatusCode >= 400 {
		return BackupStatus{}, errors.New(res.Status)
	}

	body, _ := ioutil.ReadAll(res.Body)
	taskId := string(body)

	// Get the status
	u = c.BaseURL
	u.Path = "/rest/backup/1/export/getProgress"
	q = u.Query()
	q.Set("taskId", taskId)
	q.Set("_", strconv.Itoa(int(time.Now().Unix())))
	u.RawQuery = q.Encode()

	req = &http.Request{
		Method: http.MethodGet,
		URL: &u,
		Header: http.Header{
			"Accept": []string{"application/json"},
		},
	}

	res, err = c.makeRequest(req)

	if err != nil {
		return BackupStatus{}, err
	}

	body, _ = ioutil.ReadAll(res.Body)

	var b BackupStatus
	err = json.Unmarshal(body, &b)

	return b, err

	return b, err

}