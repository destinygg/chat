package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"regexp"
)

type Api struct {
	url string
	key string
}

var (
	cookievalid = regexp.MustCompile("^[a-z0-9]{10,64}$")
	api         Api
)

func initApi(url, key string) {
	api = Api{
		url: url,
		key: key,
	}
}

func (a *Api) getUserFromAuthToken(tok string) ([]byte, error) {
	if !cookievalid.MatchString(tok) {
		return nil, fmt.Errorf("api: auth token cookie invalid %s", tok)
	}

	endpoint := a.url + "/auth"
	resp, err := http.PostForm(endpoint, url.Values{
		"authtoken":  {tok},
		"privatekey": {a.key},
	})

	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("api: auth token invalid: %s, response code: %d", tok, resp.StatusCode)
	}

	ret, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

func (a *Api) sendPrivmsg(fromuid, targetuid Userid, msg string) error {
	endpoint := a.url + "/messages/send"
	resp, err := http.PostForm(endpoint, url.Values{
		"privatekey":   {a.key},
		"userid":       {fmt.Sprintf("%d", fromuid)},
		"targetuserid": {fmt.Sprintf("%d", targetuid)},
		"message":      {msg},
	})

	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	if err != nil {
		return err
	}

	if resp.StatusCode != 204 {
		d, err := ioutil.ReadAll(resp.Body)
		if err == nil {
			var s struct {
				Error string
			}

			err = json.Unmarshal(d, &s)
			if err != nil {
				D("sendprivmsg unmarshal error", string(d))
				return fmt.Errorf("unknown")
			} else {
				return fmt.Errorf(s.Error)
			}
		}

		return fmt.Errorf("api: privmsg response code: %d", resp.StatusCode)
	}

	return nil
}
