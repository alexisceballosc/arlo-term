package arlo

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"arlo-term/internal/session"
)

const androidUA = "Mozilla/5.0 (Linux; U; Android 8.1.0; zh-cn; PACM00 Build/O11019) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/57.0.2987.132 MQQBrowser/8.8 Mobile Safari/537.36"

type Camera struct {
	ID, Name, ParentID, XCloudID string
}

type Client struct {
	session *session.Session
	userID  string
}

func Connect(s *session.Session) (*Client, []Camera, error) {
	var devices []struct {
		DeviceID   string `json:"deviceId"`
		DeviceName string `json:"deviceName"`
		DeviceType string `json:"deviceType"`
		ParentID   string `json:"parentId"`
		XCloudID   string `json:"xCloudId"`
		UserID     string `json:"userId"`
	}
	if err := s.Request("GET", "/hmsweb/v2/users/devices", nil, nil, &devices); err != nil {
		return nil, nil, err
	}
	c := &Client{session: s}
	var cameras []Camera
	for _, d := range devices {
		if c.userID == "" {
			c.userID = d.UserID
		}
		if d.DeviceType == "camera" {
			cameras = append(cameras, Camera{d.DeviceID, d.DeviceName, d.ParentID, d.XCloudID})
		}
	}
	sort.Slice(cameras, func(i, j int) bool { return cameras[i].Name < cameras[j].Name })
	return c, cameras, nil
}

func (c *Client) StreamURL(cam Camera) (string, error) {
	body := map[string]any{
		"to":              cam.ParentID,
		"from":            c.userID + "_web",
		"resource":        "cameras/" + cam.ID,
		"action":          "set",
		"responseUrl":     "",
		"publishResponse": true,
		"transId":         fmt.Sprintf("web!%x!%d", rand.Uint32(), time.Now().UnixMilli()),
		"properties":      map[string]string{"activityState": "startUserStream", "cameraId": cam.ID},
	}
	var data struct {
		URL string `json:"url"`
	}
	headers := map[string]string{"xcloudId": cam.XCloudID, "user-agent": androidUA}
	if err := c.session.Request("POST", "/hmsweb/users/devices/startStream", body, headers, &data); err != nil {
		return "", err
	}
	if data.URL == "" {
		return "", errors.New("no stream url")
	}
	return strings.Replace(data.URL, "rtsp://", "rtsps://", 1), nil
}
