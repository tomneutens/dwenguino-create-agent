// Supports Windows, Linux, Mac, and Raspberry Pi

package main

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"path/filepath"

	"arduino.cc/fromsourcebuilder"

	"arduino.cc/arduino-create-agent/upload"
	"arduino.cc/arduino-create-agent/utilities"
	log "github.com/Sirupsen/logrus"
	"github.com/gin-gonic/gin"
	"github.com/go-homedir/homedir"
	socketio "github.com/googollee/go-socket.io"
)

type connection struct {
	// The websocket connection.
	ws socketio.Socket

	// Buffered channel of outbound messages.
	send     chan []byte
	incoming chan []byte
}

func (c *connection) writer() {
	for message := range c.send {
		err := c.ws.Emit("message", string(message))
		if err != nil {
			break
		}
	}
}

// WsServer overrides socket.io server to set the CORS
type WsServer struct {
	Server *socketio.Server
}

func (s *WsServer) ServeHTTP(c *gin.Context) {
	s.Server.ServeHTTP(c.Writer, c.Request)
}

type AdditionalFile struct {
	Hex      []byte `json:"hex"`
	Filename string `json:"filename"`
}

// Upload contains the data to upload a sketch onto a board
type Upload struct {
	Port        string           `json:"port"`
	Board       string           `json:"board"`
	Rewrite     string           `json:"rewrite"`
	Commandline string           `json:"commandline"`
	Signature   string           `json:"signature"`
	Extra       upload.Extra     `json:"extra"`
	Hex         []byte           `json:"hex"`
	Filename    string           `json:"filename"`
	ExtraFiles  []AdditionalFile `json:"extrafiles"`
}

type BuildData struct {
	Code string `json:"code" binding:"required"`
}

type DwenguinoUpload struct {
	Port string `json:"port" binding:"required"`
	Hex  []byte `json:"base64HexCode" binding:"required"`
}

var uploadStatusStr string = "ProgrammerStatus"

func buildHandler(c *gin.Context) {

	var json BuildData
	err := c.BindJSON(&json)
	fmt.Println(err)

	base64Data, compilationTrace, compileError := fromsourcebuilder.ExecuteBuild(json.Code)

	status := ""

	if compileError != nil {
		status = "An error has occured during compilation: " + compileError.Error()
	} else {
		status = "compilation succesfull"
	}

	c.JSON(200, gin.H{
		"status":           status,
		"base64HexCode":    base64Data,
		"compilationTrace": compilationTrace,
	})
}

func listPortsHandler(c *gin.Context) {
	// list serial ports
	portList, _ := GetList(false)
	log.Println("Your serial ports:")
	if len(portList) == 0 {
		log.Println("\tThere are no serial ports to list.")
	}
	for _, element := range portList {
		log.Printf("\t%v\n", element)
	}
	c.JSON(200, gin.H{
		"portsList": portList,
	})
}

func uploadToDwenguinoHandler(c *gin.Context) {
	var dwUpload DwenguinoUpload
	err := c.BindJSON(&dwUpload)

	if err != nil {
		c.String(http.StatusBadRequest, "Unable to bind json")
	}

	var upl Upload
	upl.Hex = dwUpload.Hex
	upl.Port = dwUpload.Port
	upl.Board = "Dwenguino:avr:dwenguino"
	upl.Extra.Network = false
	upl.Filename = "sketch.ino.hex"

	var home, shit = homedir.Dir()
	if shit != nil {
		fmt.Println("NOOOOOOO, your home directory was not found")
	}
	upl.Commandline = home + "/dwenguino/build_input/packages/arduino/tools/avrdude/6.0.1-arduino5/bin/avrdude -C " + home + "/dwenguino/build_input/packages/arduino/tools/avrdude/6.0.1-arduino5/etc/avrdude.conf {upload.verbose} -p at90usb646 -c avr109 -P {serial.port} -b 57600 -D -U flash:w:{build.path}/{build.project_name}.hex:i"

	handleUpload(upl, c)
}

func uploadHandler(c *gin.Context) {

	var data Upload
	err := c.BindJSON(&data)

	if err != nil {
		c.String(http.StatusBadRequest, "Unable to bind json")
	}

	// Perform singature check
	if data.Extra.Network == false {
		if data.Signature == "" {
			c.String(http.StatusBadRequest, "signature is required")
			return
		}

		if data.Commandline == "" {
			c.String(http.StatusBadRequest, "commandline is required for local board")
			return
		}

		err := verifyCommandLine(data.Commandline, data.Signature)

		if err != nil {
			c.String(http.StatusBadRequest, "signature is invalid")
			return
		}
	}

	fmt.Println(data)

	log.Printf("%+v", data)

	handleUpload(data, c)

}

func handleUpload(data Upload, c *gin.Context) {
	if data.Port == "" {
		c.String(http.StatusBadRequest, "port is required")
		return
	}

	if data.Board == "" {
		c.String(http.StatusBadRequest, "board is required")
		log.Error("board is required")
		return
	}

	buffer := bytes.NewBuffer(data.Hex)

	filePath, err := utilities.SaveFileonTempDir(data.Filename, buffer)
	if err != nil {
		fmt.Println("NOOOOOOOOO")
		c.String(http.StatusBadRequest, err.Error())
		return
	}

	var filePaths []string
	filePaths = append(filePaths, filePath)

	for _, extraFile := range data.ExtraFiles {
		path := filepath.Join(filepath.Dir(filePath), extraFile.Filename)
		filePaths = append(filePaths, path)
		log.Printf("Saving %s on %s", extraFile.Filename, path)
		err := ioutil.WriteFile(path, extraFile.Hex, 0644)
		if err != nil {
			log.Printf(err.Error())
		}
	}

	if data.Rewrite != "" {
		data.Board = data.Rewrite
	}

	go func() {
		// Resolve commandline
		commandline, err := upload.PartiallyResolve(data.Board, filePath, data.Commandline, data.Extra, &Tools)
		if err != nil {
			send(map[string]string{uploadStatusStr: "Error", "Msg": err.Error()})
			return
		}

		l := PLogger{Verbose: data.Extra.Verbose}

		// Upload
		if data.Extra.Network {
			send(map[string]string{uploadStatusStr: "Starting", "Cmd": "Network"})
			err = upload.Network(data.Port, data.Board, filePaths, commandline, data.Extra.Auth, l, data.Extra.SSH)
		} else {
			send(map[string]string{uploadStatusStr: "Starting", "Cmd": "Serial"})
			err = upload.Serial(data.Port, commandline, data.Extra, l)
		}

		// Handle result
		if err != nil {
			send(map[string]string{uploadStatusStr: "Error", "Msg": err.Error()})
			return
		}
		send(map[string]string{uploadStatusStr: "Done", "Flash": "Ok"})
	}()

	c.String(http.StatusAccepted, "")
}

// PLogger sends the info from the upload to the websocket
type PLogger struct {
	Verbose bool
}

// Debug only sends messages if verbose is true
func (l PLogger) Debug(args ...interface{}) {
	if l.Verbose {
		l.Info(args...)
	}
}

// Info always send messages
func (l PLogger) Info(args ...interface{}) {
	output := fmt.Sprint(args...)
	log.Println(output)
	send(map[string]string{uploadStatusStr: "Busy", "Msg": output})
}

func send(args map[string]string) {
	mapB, _ := json.Marshal(args)
	h.broadcastSys <- mapB
}

func verifyCommandLine(input string, signature string) error {
	sign, _ := hex.DecodeString(signature)
	block, _ := pem.Decode([]byte(*signatureKey))
	if block == nil {
		return errors.New("invalid key")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return err
	}
	rsaKey := key.(*rsa.PublicKey)
	h := sha256.New()
	h.Write([]byte(input))
	d := h.Sum(nil)
	return rsa.VerifyPKCS1v15(rsaKey, crypto.SHA256, d, sign)
}

func wsHandler() *WsServer {
	server, err := socketio.NewServer(nil)
	if err != nil {
		log.Fatal(err)
	}

	server.On("connection", func(so socketio.Socket) {
		c := &connection{send: make(chan []byte, 256*10), ws: so}
		h.register <- c
		so.On("command", func(message string) {
			h.broadcast <- []byte(message)
		})

		so.On("disconnection", func() {
			h.unregister <- c
		})
		go c.writer()
	})
	server.On("error", func(so socketio.Socket, err error) {
		log.Println("error:", err)
	})

	wrapper := WsServer{
		Server: server,
	}

	return &wrapper
}
