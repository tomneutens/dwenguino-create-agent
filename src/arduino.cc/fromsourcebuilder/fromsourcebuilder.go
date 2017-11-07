package fromsourcebuilder

import (
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"syscall"

	"arduino.cc/builder"
	"arduino.cc/builder/i18n"
	"arduino.cc/builder/types"
	"github.com/go-errors/errors"
	"github.com/go-homedir/homedir"

	"arduino.cc/builder/gohasissues"
)

/**
* @brief This function builds sketchCode
* @param The code string to be Built
* @return The base64 encoding of the generated hex file
* @return The compilation trace
* @return the error or nil
**/
// ExecuteBuild executes the build process from source
func ExecuteBuild(sketchCode string) (string, string, error) {

	// Get configuration folders
	var home, err = homedir.Dir()
	var buildPath = home + "/dwenguino/build_output/"
	var hardwareFolders string
	hardwareFolders = home + "/dwenguino/build_input/packages"
	var toolsFolders string
	toolsFolders = hardwareFolders
	var librariesFolders string
	librariesFolders = home + "/dwenguino/build_input/libraries"

	ctx := &types.Context{}

	// Set context
	ctx.HardwareFolders = append(ctx.HardwareFolders, hardwareFolders)
	ctx.ToolsFolders = append(ctx.ToolsFolders, toolsFolders)
	ctx.OtherLibrariesFolders = append(ctx.OtherLibrariesFolders, librariesFolders)
	ctx.FQBN = "dwenguino:avr:Dwenguino"
	ctx.BuildPath = buildPath

	// Write sketch code to temp file, first check if sketch dir exists, if not create it
	if _, noooo := os.Stat(home + "/dwenguino/sketch"); os.IsNotExist(noooo) {
		os.Mkdir(home+"/dwenguino/sketch", os.ModePerm)
	}
	file, err := os.Create(home + "/dwenguino/sketch/sketch.ino")
	if err != nil {
		panic(err)
	}
	file.WriteString(sketchCode)
	file.Sync()

	// close fi on exit and check for its returned error
	defer func() {
		if awtch := file.Close(); awtch != nil {
			panic(awtch)
		}
	}()

	// Get the location of the temp sketch file
	loc, oops := gohasissues.Unquote(file.Name())
	if oops != nil {
		return "", "", oops
	}

	// More context settings
	ctx.SketchLocation = loc
	ctx.Verbose = false
	ctx.ArduinoAPIVersion = "10600"
	ctx.DebugLevel = 0
	ctx.DebugPreprocessor = false

	// Set logger to log everything into a string constant so it can be sent back to the user
	toSourceLogger := i18n.NewToSourceLogger()
	ctx.SetLogger(toSourceLogger)

	// Run builder
	err = builder.RunBuilder(ctx)

	// get logger output
	compilationTrace := toSourceLogger.PopLog()

	// Check for errors
	if err != nil {
		err = i18n.WrapError(err)

		fmt.Fprintln(os.Stderr, err)

		if ctx.DebugLevel >= 10 {
			fmt.Fprintln(os.Stderr, err.(*errors.Error).ErrorStack())
		}
		return "", compilationTrace, err
		//os.Exit(toExitCode(err))
	}

	// Read hex file and convert into base 64 string
	data, err := ioutil.ReadFile(buildPath + "sketch.ino.hex")
	if err != nil {
		log.Fatal(err)
	}
	base64Data := base64.StdEncoding.EncodeToString(data)

	// Remove the temp files
	defer os.Remove(file.Name())
	defer os.Remove(buildPath + "sketch.ino.hex")
	defer os.Remove(buildPath + "sketch.ino.elf")

	return base64Data, compilationTrace, nil

}

func toExitCode(err error) int {
	if exiterr, ok := err.(*exec.ExitError); ok {
		if status, ok := exiterr.Sys().(syscall.WaitStatus); ok {
			return status.ExitStatus()
		}
	}
	return 1
}
