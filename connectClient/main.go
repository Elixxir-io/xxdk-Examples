// Sending Normal messages (Getting Started guide)
package main

import (
	"errors"
	"fmt"
	jww "github.com/spf13/jwalterweatherman"
	"gitlab.com/elixxir/client/catalog"
	"gitlab.com/elixxir/client/xxdk"
	"io/fs"
	"io/ioutil"
	"os"
	"time"

	"gitlab.com/elixxir/client/connect"
	"gitlab.com/elixxir/crypto/contact"
)

func main() {
	// Logging
	initLog(1, "client.log")

	// Create a new client object-------------------------------------------------------

	// Path to the server contact file
	serverContactPath := "server-contact.json"

	// You would ideally use a configuration tool to acquire these parameters
	statePath := "statePath"
	statePass := "password"
	// The following connects to mainnet. For historical reasons it is called a json file
	// but it is actually a marshalled file with a cryptographic signature attached.
	// This may change in the future.
	ndfURL := "https://elixxir-bins.s3.us-west-1.amazonaws.com/ndf/mainnet.json"
	certificatePath := "../mainnet.crt"
	ndfPath := "ndf.json"

	// Check if state exists
	if _, err := os.Stat(statePath); errors.Is(err, fs.ErrNotExist) {

		// Attempt to read the NDF
		var ndfJSON []byte
		ndfJSON, err = ioutil.ReadFile(ndfPath)
		if err != nil {
			jww.INFO.Printf("NDF does not exist: %+v", err)
		}

		// If NDF can't be read, retrieve it remotely
		if ndfJSON == nil {
			cert, err := ioutil.ReadFile(certificatePath)
			if err != nil {
				jww.FATAL.Panicf("Failed to read certificate: %v", err)
			}

			ndfJSON, err = xxdk.DownloadAndVerifySignedNdfWithUrl(ndfURL, string(cert))
			if err != nil {
				jww.FATAL.Panicf("Failed to download NDF: %+v", err)
			}
		}

		// Initialize the state
		err = xxdk.NewCmix(string(ndfJSON), statePath, []byte(statePass), "")
		if err != nil {
			jww.FATAL.Panicf("Failed to initialize state: %+v", err)
		}
	}

	// Login to your client session-----------------------------------------------------

	// Login with the same sessionPath and sessionPass used to call NewClient()
	baseClient, err := xxdk.LoadCmix(statePath, []byte(statePass), xxdk.GetDefaultCMixParams())
	if err != nil {
		jww.FATAL.Panicf("Failed to load state: %+v", err)
	}

	// Get reception identity (automatically created if one does not exist)
	identityStorageKey := "identityStorageKey"
	identity, err := xxdk.LoadReceptionIdentity(identityStorageKey, baseClient)
	if err != nil {
		// If no extant xxdk.ReceptionIdentity, generate and store a new one
		identity, err = xxdk.MakeReceptionIdentity(baseClient)
		if err != nil {
			jww.FATAL.Panicf("Failed to generate reception identity: %+v", err)
		}
		err = xxdk.StoreReceptionIdentity(identityStorageKey, identity, baseClient)
		if err != nil {
			jww.FATAL.Panicf("Failed to store new reception identity: %+v", err)
		}
	}

	// Create an E2E client
	// The connect packages handles AuthCallbacks, xxdk.DefaultAuthCallbacks is fine here
	params := xxdk.GetDefaultE2EParams()
	jww.INFO.Printf("Using E2E parameters: %+v", params)
	e2eClient, err := xxdk.Login(baseClient, xxdk.DefaultAuthCallbacks{}, identity, params)
	if err != nil {
		jww.FATAL.Panicf("Unable to Login: %+v", err)
	}

	// Start network threads------------------------------------------------------------

	// Set networkFollowerTimeout to a value of your choice (seconds)
	networkFollowerTimeout := 5 * time.Second
	err = baseClient.StartNetworkFollower(networkFollowerTimeout)
	if err != nil {
		fmt.Printf("Failed to start network follower: %+v", err)
	}

	// Set up a wait for the network to be connected
	waitUntilConnected := func(connected chan bool) {
		waitTimeout := 30 * time.Second
		timeoutTimer := time.NewTimer(waitTimeout)
		isConnected := false
		// Wait until we connect or panic if we cannot before the timeout
		for !isConnected {
			select {
			case isConnected = <-connected:
				jww.INFO.Printf("Network Status: %v", isConnected)
				break
			case <-timeoutTimer.C:
				jww.FATAL.Panicf("Timeout on starting network follower")
			}
		}
	}

	// Create a tracker channel to be notified of network changes
	connected := make(chan bool, 10)
	// Provide a callback that will be signalled when network health status changes
	baseClient.GetCmix().AddHealthCallback(
		func(isConnected bool) {
			connected <- isConnected
		})
	// Wait until connected or crash on timeout
	waitUntilConnected(connected)

	// Connect with the server--------------------------------------------------

	// Recipient's contact (read from a Client CLI-generated contact file)
	contactData, err := ioutil.ReadFile(serverContactPath)
	if err != nil {
		jww.FATAL.Panicf("Failed to read server contact file: %+v", err)
	}

	// Imported "gitlab.com/elixxir/crypto/contact"
	// which provides an `Unmarshal` function to convert the byte slice ([]byte) output
	// of `ioutil.ReadFile()` to the `Contact` type expected by `RequestAuthenticatedChannel()`
	recipientContact, err := contact.Unmarshal(contactData)
	if err != nil {
		jww.FATAL.Panicf("Failed to get contact data: %+v", err)
	}
	jww.INFO.Printf("Recipient contact: %+v", recipientContact)

	// Create the connection
	handler, err := connect.Connect(recipientContact, e2eClient, params)
	if err != nil {
		jww.FATAL.Panicf("Failed to create connection object: %+v", err)
	}
	jww.INFO.Printf("Connect with %s successfully established!", recipientContact.ID)

	// Register a listener for messages--------------------------------------------------

	// Listen for all types of messages using catalog.NoType
	_, err = handler.RegisterListener(catalog.NoType, listener{
		name: "e2e Message Listener",
	})
	if err != nil {
		jww.FATAL.Panicf("Could not register message listener: %+v", err)
	}

	// Send a message to the server----------------------------------------------------

	// Test message
	msgBody := "If this message is sent successfully, we'll have established first contact with aliens."
	roundIDs, messageID, timeSent, err := handler.SendE2E(catalog.XxMessage, []byte(msgBody), params.Base)
	if err != nil {
		fmt.Printf("Failed to send message: %+v", err)
	}
	jww.INFO.Printf("Message %v sent in RoundIDs: %+v at %v", messageID, roundIDs, timeSent)

	// Keep app running to receive messages-----------------------------------------------

	select {}
}