package main

import (
	"github.com/pkg/errors"
	jww "github.com/spf13/jwalterweatherman"
	"gitlab.com/elixxir/client/restlike/single"
	"gitlab.com/elixxir/client/xxdk"
	"gitlab.com/elixxir/crypto/cyclic"
	"io/fs"
	"io/ioutil"
	"os"
	"time"
)

func main() {
	// Logging
	initLog(1, "client.log")

	// Create a new client object----------------------------------------------
	// NOTE: For some (or all) of these parameters, you may want to use a
	// configuration tool of some kind

	// Set the output contact file path
	contactFilePath := "restlikeServer.xxc"

	// Set state file parameters
	statePath := "statePath"
	statePass := "password"

	// The following connects to mainnet. For historical reasons
	// it is called a json file but it is actually a marshalled
	// file with a cryptographic signature attached.
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

			ndfJSON, err = xxdk.DownloadAndVerifySignedNdfWithUrl(ndfURL,
				string(cert))
			if err != nil {
				jww.FATAL.Panicf("Failed to download NDF: %+v", err)
			}
		}

		// Initialize the state using the state file
		err = xxdk.NewCmix(string(ndfJSON), statePath, []byte(statePass), "")
		if err != nil {
			jww.FATAL.Panicf("Failed to initialize state: %+v", err)
		}
	}

	// Login to your client session--------------------------------------------

	// Login with the same sessionPath and sessionPass used to call NewClient()
	baseClient, err := xxdk.LoadCmix(statePath, []byte(statePass),
		xxdk.GetDefaultCMixParams())
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
	// The 'restlike' package handles AuthCallbacks,
	// xxdk.DefaultAuthCallbacks is fine here
	params := xxdk.GetDefaultE2EParams()
	jww.INFO.Printf("Using E2E parameters: %+v", params)
	e2eClient, err := xxdk.Login(baseClient, xxdk.DefaultAuthCallbacks{},
		identity, params)
	if err != nil {
		jww.FATAL.Panicf("Unable to Login: %+v", err)
	}

	// Save contact file-------------------------------------------------------

	// Save the contact file so that client can connect to this server
	writeContact(contactFilePath, identity.GetContact())

	// Start rest-like single use server---------------------------------------

	// Pull the reception
	receptionIdentity := e2eClient.GetReceptionIdentity()
	dhKeyPriv := &cyclic.Int{}
	err = dhKeyPriv.UnmarshalJSON(receptionIdentity.DHKeyPrivate)
	if err != nil {
		jww.FATAL.Panicf("Unable to read diffie-hellman private key: %+v", err)
	}

	// Initialize the server
	single.NewServer(receptionIdentity.ID, dhPrivateKey,
		e2eClient.GetE2E().GetGroup(), e2eClient.GetCmix())

	// Start network threads---------------------------------------------------

	// Set networkFollowerTimeout to a value of your choice (seconds)
	networkFollowerTimeout := 5 * time.Second
	err = e2eClient.StartNetworkFollower(networkFollowerTimeout)
	if err != nil {
		jww.FATAL.Panicf("Failed to start network follower: %+v", err)
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
	// Provide a callback that will be signalled when network
	// health status changes
	e2eClient.GetCmix().AddHealthCallback(
		func(isConnected bool) {
			connected <- isConnected
		})
	// Wait until connected or crash on timeout
	waitUntilConnected(connected)

	// Keep app running to receive messages------------------------------------

	// NOTE: Some consumers may desire some implemented exit condition
	// handling (SIGKILL, SIGABRT, etc.). That can be done here.
	select {}

}
