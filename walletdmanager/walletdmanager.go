// Package walletdmanager handles the management of the wallet and the communication with the core wallet software
package walletdmanager

import (
	"TurtleCoin-Nest/turtlecoinwalletdrpcgo"
	"bufio"
	"errors"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/mitchellh/go-ps"
)

var (
	logWalletdCurrentSessionFilename = "walletdCurrentSession.log"
	logWalletdAllSessionsFilename    = "walletd.log"

	walletdCommandName     = "walletd"
	turtlecoindCommandName = "TurtleCoind"

	// WalletAvailableBalance is the available balance
	WalletAvailableBalance float64

	// WalletAddress is the wallet address
	WalletAddress string

	// WalletFilename is the filename of the opened wallet
	WalletFilename = ""

	// will be set to a random string when starting walletd
	rpcPassword = ""

	cmdWalletd *exec.Cmd

	// WalletdOpenAndRunning is true when walletd is running with a wallet open
	WalletdOpenAndRunning = false

	// WalletdSynced is true when wallet is synced and transfer is allowed
	WalletdSynced = false

	isPlatformDarwin  = false
	isPlatformLinux   = true
	isPlatformWindows = false
)

// Setup sets up some settings. It must be called at least once at the beginning of your program.
// platform should be set based on your platform. The choices are "linux", "darwin", "windows"
func Setup(platform string) {

	isPlatformDarwin = false
	isPlatformLinux = false
	isPlatformWindows = false

	switch platform {
	case "darwin":
		isPlatformDarwin = true
	case "windows":
		isPlatformWindows = true
	case "linux":
		isPlatformLinux = true
	default:
		isPlatformLinux = true
	}

}

// RequestBalance provides the available and locked balances of the current wallet
func RequestBalance() (availableBalance float64, lockedBalance float64, totalBalance float64, err error) {

	availableBalance, lockedBalance, totalBalance, err = turtlecoinwalletdrpcgo.RequestBalance(rpcPassword)
	if err != nil {
		log.Error("error requesting balances. err: ", err)
	} else {
		WalletAvailableBalance = availableBalance
	}
	return availableBalance, lockedBalance, totalBalance, err
}

// RequestAddress provides the address of the current wallet
func RequestAddress() (address string, err error) {

	address, err = turtlecoinwalletdrpcgo.RequestAddress(rpcPassword)
	if err != nil {
		log.Error("error requesting address. err: ", err)
	} else {
		WalletAddress = address
	}
	return address, err
}

// RequestListTransactions provides the list of transactions of current wallet
func RequestListTransactions() (transfers []turtlecoinwalletdrpcgo.Transfer, err error) {

	walletBlockCount, _, _, err := turtlecoinwalletdrpcgo.RequestStatus(rpcPassword)
	if err != nil {
		log.Error("error getting block count: ", err)
		return nil, err
	}

	transfers, err = turtlecoinwalletdrpcgo.RequestListTransactions(walletBlockCount, 1, []string{WalletAddress}, rpcPassword)
	if err != nil {
		log.Error("error requesting list transactions. err: ", err)
	}
	return transfers, err
}

// SendTransaction makes a transfer with the provided information
func SendTransaction(transferAddress string, transferAmountString string, transferPaymentID string) (transactionHash string, err error) {

	if !strings.HasPrefix(transferAddress, "TRTL") || len(transferAddress) != 99 {
		return "", errors.New("address is invalid")
	}

	if transferAddress == WalletAddress {
		return "", errors.New("sending to yourself is not supported")
	}

	var transferFee float64 = 1 // transferFee is expressed in TRTL
	transferMixin := 4
	transferAmount, err := strconv.ParseFloat(transferAmountString, 64) // transferAmount is expressed in TRTL
	if err != nil {
		return "", errors.New("amount is invalid")
	}

	if transferAmount <= 0 {
		return "", errors.New("amount of TRTL to be sent should be greater than 0")
	}

	if transferAmount+transferFee > WalletAvailableBalance {
		return "", errors.New("your available balance is insufficient")
	}

	if transferAmount > 5000000 {
		return "", errors.New("for sending more than 5,000,000 TRTL to one address, you should split in multiple transfers of smaller amounts")
	}

	transactionHash, err = turtlecoinwalletdrpcgo.SendTransaction(transferAddress, transferAmount, transferPaymentID, transferFee, transferMixin, rpcPassword)
	if err != nil {
		log.Error("error sending transaction. err: ", err)
	}
	return transactionHash, err

}

// GetPrivateViewKeyAndSpendKey provides the private view and spend keys of the current wallet
func GetPrivateViewKeyAndSpendKey() (privateViewKey string, privateSpendKey string, err error) {

	privateViewKey, err = turtlecoinwalletdrpcgo.GetViewKey(rpcPassword)
	if err != nil {
		log.Error("error requesting view key. err: ", err)
		return "", "", err
	}

	privateSpendKey, _, err = turtlecoinwalletdrpcgo.GetSpendKeys(WalletAddress, rpcPassword)
	if err != nil {
		log.Error("error requesting spend keys. err: ", err)
		return "", "", err
	}

	return privateViewKey, privateSpendKey, nil
}

// StartWalletd starts the walletd daemon with the set wallet info
// walletPath is the full path to the wallet
// walletPassword is the wallet password
func StartWalletd(walletPath string, walletPassword string) (err error) {

	fileExtension := filepath.Ext(walletPath)

	if fileExtension != ".wallet" {

		return errors.New("filename should end with .wallet")

	}

	if isWalletdRunning() {

		errorMessage := "Walletd or TurtleCoind is already running in the background.\nPlease close it via "

		if isPlatformWindows {
			errorMessage += "the task manager"
		} else if isPlatformDarwin {
			errorMessage += "the activity monitor"
		} else if isPlatformLinux {
			errorMessage += "a system monitor app"
		}
		errorMessage += "."

		return errors.New(errorMessage)

	}

	pathToLogWalletdCurrentSession := logWalletdCurrentSessionFilename
	pathToLogWalletdAllSessions := logWalletdAllSessionsFilename
	pathToWalletd := "./" + walletdCommandName

	WalletFilename = filepath.Base(walletPath)
	pathToWallet := filepath.Clean(walletPath)

	if isPlatformWindows {

		pathToWallet = strings.Replace(pathToWallet, "file:\\", "", 1)

	} else {

		pathToWallet = strings.Replace(pathToWallet, "file:", "", 1)

	}

	if isPlatformDarwin {

		currentDirectory, err := filepath.Abs(filepath.Dir(os.Args[0]))
		if err != nil {
			log.Fatal("error finding current directory. Error: ", err)
		}
		pathToAppContents := filepath.Dir(currentDirectory)
		pathToWalletd = pathToAppContents + "/" + walletdCommandName

		usr, err := user.Current()
		if err != nil {
			log.Fatal("error finding home directory. Error: ", err)
		}
		pathToHomeDir := usr.HomeDir
		pathToAppLibDir := pathToHomeDir + "/Library/Application Support/TurtleCoin-Nest"

		pathToLogWalletdCurrentSession = pathToAppLibDir + "/" + logWalletdCurrentSessionFilename
		pathToLogWalletdAllSessions = pathToAppLibDir + "/" + logWalletdAllSessionsFilename

		if pathToWallet == WalletFilename {
			// if comes from createWallet, so it is not a full path, just a filename
			pathToWallet = pathToHomeDir + "/" + pathToWallet
		}
	}

	// setup current session log file (logs are added real time in this file)
	walletdCurrentSessionLogFile, err := os.Create(pathToLogWalletdCurrentSession)
	if err != nil {
		log.Error(err)
	}
	defer walletdCurrentSessionLogFile.Close()

	rpcPassword = randStringBytesMaskImprSrc(20)

	cmdWalletd = exec.Command(pathToWalletd, "-w", pathToWallet, "-p", walletPassword, "-l", pathToLogWalletdCurrentSession, "--local", "--rpc-password", rpcPassword)

	// setup all sessions log file
	walletdAllSessionsLogFile, err := os.OpenFile(pathToLogWalletdAllSessions, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		log.Fatal(err)
	}
	cmdWalletd.Stdout = walletdAllSessionsLogFile
	defer walletdAllSessionsLogFile.Close()

	err = cmdWalletd.Start()

	if err != nil {
		log.Error(err)
		return err
	}

	time.Sleep(5 * time.Second)

	reader := bufio.NewReader(walletdCurrentSessionLogFile)

	var listWalletdErrors []string

	for {

		line, err := reader.ReadString('\n')

		if err != nil {

			if err != io.EOF {

				log.Error("Failed reading log file line by line: ", err)

			}

			break
		}

		identifierErrorMessage := " ERROR  "
		if strings.Contains(line, identifierErrorMessage) {

			splitLine := strings.Split(line, identifierErrorMessage)
			listWalletdErrors = append(listWalletdErrors, splitLine[len(splitLine)-1])

		}

	}

	errorMessage := "Error opening the daemon walletd. Could be a problem with your wallet file, your password or walletd. More info in the file " + logWalletdAllSessionsFilename + "\n"

	if len(listWalletdErrors) > 0 {

		for _, line := range listWalletdErrors {

			errorMessage = errorMessage + line

		}

	}

	// check rpc connection with walletd
	_, _, _, err = turtlecoinwalletdrpcgo.RequestStatus(rpcPassword)

	if err != nil {

		killWalletd()

		return errors.New(errorMessage)

	}

	WalletdOpenAndRunning = true

	return nil
}

// GracefullyQuitWalletd stops the walletd daemon
func GracefullyQuitWalletd() {

	if WalletdOpenAndRunning && cmdWalletd != nil {

		var err error

		if isPlatformWindows {
			// because syscall.SIGTERM does not work in windows. We have to manually save the wallet, as we kill walletd.
			turtlecoinwalletdrpcgo.SaveWallet(rpcPassword)
			time.Sleep(3 * time.Second)

			err = cmdWalletd.Process.Kill()
			if err != nil {
				log.Error("failed to kill walletd: " + err.Error())
			} else {
				log.Info("walletd killed without error")
			}
		} else {
			_ = cmdWalletd.Process.Signal(syscall.SIGTERM)
			done := make(chan error, 1)
			go func() {
				done <- cmdWalletd.Wait()
			}()
			select {
			case <-time.After(5 * time.Second):
				if err := cmdWalletd.Process.Kill(); err != nil {
					log.Warning("failed to kill walletd: " + err.Error())
				}
				log.Info("Walletd killed as stopping process timed out")
			case err := <-done:
				if err != nil {
					log.Warning("Walletd finished with error: " + err.Error())
				}
				log.Info("Walletd killed without error")
			}
		}
	}

	WalletAvailableBalance = 0
	WalletAddress = ""
	WalletFilename = ""
	cmdWalletd = nil
	WalletdOpenAndRunning = false

}

// to make sure that after creating a wallet, there is no walletd process remaining at all
func killWalletd() {

	if cmdWalletd != nil {

		if isPlatformWindows {

			cmdWalletd.Process.Kill()

		} else {

			done := make(chan error, 1)
			go func() {
				done <- cmdWalletd.Wait()
			}()
			select {
			case <-time.After(500 * time.Millisecond):
				if err := cmdWalletd.Process.Kill(); err != nil {
					log.Warning("failed to kill walletd: " + err.Error())
				}
				log.Info("Walletd killed as stopping process timed out")
			case err := <-done:
				if err != nil {
					log.Warning("Walletd finished with error: " + err.Error())
				}
				log.Info("Walletd killed without error")
			}

		}

	}

}

// CreateWallet calls walletd to create a new wallet. If privateViewKey and privateSpendKey are empty strings, a new wallet will be generated. If they are not empty, a wallet will be generated from those keys (import)
// walletFilename is the filename chosen by the user. The created wallet file will be located in the same folder as walletd.
// walletPassword is the password of the new wallet.
// privateViewKey is the private view key of the wallet.
// privateSpendKey is the private spend key of the wallet.
func CreateWallet(walletFilename string, walletPassword string, privateViewKey string, privateSpendKey string) (err error) {

	if WalletdOpenAndRunning {
		return errors.New("walletd is already running. It should be stopped before being able to generate a new wallet")
	}

	if strings.Contains(walletFilename, "/") || strings.Contains(walletFilename, " ") || strings.Contains(walletFilename, ":") {
		return errors.New("you should avoid spaces and most special characters in the filename")
	}

	if isWalletdRunning() {
		errorMessage := "Walletd or TurtleCoind is already running in the background.\nPlease close it via "

		if isPlatformWindows {
			errorMessage += "the task manager"
		} else if isPlatformDarwin {
			errorMessage += "the activity monitor"
		} else if isPlatformLinux {
			errorMessage += "a system monitor app"
		}
		errorMessage += "."

		return errors.New(errorMessage)
	}

	pathToLogWalletdCurrentSession := logWalletdCurrentSessionFilename
	pathToLogWalletdAllSessions := logWalletdAllSessionsFilename
	pathToWalletd := "./" + walletdCommandName
	pathToWallet := walletFilename

	if isPlatformDarwin {

		currentDirectory, err := filepath.Abs(filepath.Dir(os.Args[0]))
		if err != nil {
			log.Fatal("error finding current directory. Error: ", err)
		}
		pathToAppContents := filepath.Dir(currentDirectory)
		pathToWalletd = pathToAppContents + "/" + walletdCommandName

		usr, err := user.Current()
		if err != nil {
			log.Fatal("error finding home directory. Error: ", err)
		}
		pathToHomeDir := usr.HomeDir
		pathToAppLibDir := pathToHomeDir + "/Library/Application Support/TurtleCoin-Nest"

		pathToLogWalletdCurrentSession = pathToAppLibDir + "/" + logWalletdCurrentSessionFilename
		pathToLogWalletdAllSessions = pathToAppLibDir + "/" + logWalletdAllSessionsFilename
		pathToWallet = pathToHomeDir + "/" + walletFilename
	}

	// setup current session log file (logs are added real time in this file)
	walletdCurrentSessionLogFile, err := os.Create(pathToLogWalletdCurrentSession)
	if err != nil {
		log.Error(err)
	}
	defer walletdCurrentSessionLogFile.Close()

	if privateViewKey == "" && privateSpendKey == "" {
		// generate new wallet
		cmdWalletd = exec.Command(pathToWalletd, "-w", pathToWallet, "-p", walletPassword, "-l", pathToLogWalletdCurrentSession, "-g")
	} else {
		// import wallet from private view and spend keys
		cmdWalletd = exec.Command(pathToWalletd, "-w", pathToWallet, "-p", walletPassword, "--view-key", privateViewKey, "--spend-key", privateSpendKey, "-l", pathToLogWalletdCurrentSession, "-g")
	}

	// setup all sessions log file
	walletdAllSessionsLogFile, err := os.OpenFile(pathToLogWalletdAllSessions, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		log.Fatal(err)
	}
	cmdWalletd.Stdout = walletdAllSessionsLogFile
	defer walletdAllSessionsLogFile.Close()

	err = cmdWalletd.Start()

	if err != nil {
		log.Error(err)
		return err
	}

	time.Sleep(5 * time.Second)

	reader := bufio.NewReader(walletdCurrentSessionLogFile)

	var listWalletdErrors []string

	successCreatingWallet := false

	for {

		line, err := reader.ReadString('\n')

		if err != nil {

			if err != io.EOF {

				log.Error("Failed reading log file line by line: ", err)

			}

			break
		}

		identifierErrorMessage := " ERROR  "
		if strings.Contains(line, identifierErrorMessage) {

			splitLine := strings.Split(line, identifierErrorMessage)
			listWalletdErrors = append(listWalletdErrors, splitLine[len(splitLine)-1])

		} else {

			identifierErrorMessage = "error: "
			if strings.Contains(line, identifierErrorMessage) {

				splitLine := strings.Split(line, identifierErrorMessage)
				listWalletdErrors = append(listWalletdErrors, splitLine[len(splitLine)-1])

			}

		}

		if strings.Contains(line, "New wallet is generated. Address:") || strings.Contains(line, "New wallet added") {

			successCreatingWallet = true

			break

		}

		killWalletd()

		time.Sleep(1 * time.Second)

	}

	errorMessage := "Error opening walletd and/or creating a wallet. More info in the file " + logWalletdAllSessionsFilename + "\n"

	if !successCreatingWallet {

		if len(listWalletdErrors) > 0 {

			for _, line := range listWalletdErrors {

				errorMessage = errorMessage + line

			}

		}

		killWalletd()

		return errors.New(errorMessage)

	}

	return nil

}

// RequestConnectionInfo provides the blockchain sync status and the number of connected peers
func RequestConnectionInfo() (syncing string, blockCountString string, knownBlockCountString string, peerCountString string, err error) {

	blockCount, knownBlockCount, peerCount, err := turtlecoinwalletdrpcgo.RequestStatus(rpcPassword)
	if err != nil {
		return "", "", "", "", err
	}

	stringWait := " (No transfers allowed until synced)"
	if knownBlockCount == 0 {
		WalletdSynced = false
		syncing = "Getting block count..." + stringWait
	} else if blockCount < knownBlockCount-1 || blockCount > knownBlockCount+10 {
		// second condition handles cases when knownBlockCount is off and smaller than the blockCount
		WalletdSynced = false
		syncing = "Wallet syncing..." + stringWait
	} else {
		WalletdSynced = true
		syncing = "Wallet synced"
	}

	return syncing, strconv.Itoa(blockCount), strconv.Itoa(knownBlockCount), strconv.Itoa(peerCount), nil
}

// generate a random string with n characters. from https://stackoverflow.com/a/31832326/1668837
func randStringBytesMaskImprSrc(n int) string {

	const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	const letterIdxBits = 6                    // 6 bits to represent a letter index
	const letterIdxMask = 1<<letterIdxBits - 1 // All 1-bits, as many as letterIdxBits
	const letterIdxMax = 63 / letterIdxBits    // # of letter indices fitting in 63 bits

	src := rand.NewSource(time.Now().UnixNano())
	b := make([]byte, n)
	// A src.Int63() generates 63 random bits, enough for letterIdxMax characters!
	for i, cache, remain := n-1, src.Int63(), letterIdxMax; i >= 0; {
		if remain == 0 {
			cache, remain = src.Int63(), letterIdxMax
		}
		if idx := int(cache & letterIdxMask); idx < len(letterBytes) {
			b[i] = letterBytes[idx]
			i--
		}
		cache >>= letterIdxBits
		remain--
	}

	return string(b)
}

// find process in the running processes of the system (github.com/mitchellh/go-ps)
func findProcess(key string) (int, string, error) {
	pname := ""
	pid := 0
	err := errors.New("not found")
	ps, _ := ps.Processes()

	for i := range ps {
		if ps[i].Executable() == key {
			pid = ps[i].Pid()
			pname = ps[i].Executable()
			err = nil
			break
		}
	}
	return pid, pname, err
}

func isWalletdRunning() bool {

	if _, _, err := findProcess(walletdCommandName); err == nil {
		return true
	}
	if _, _, err := findProcess(turtlecoindCommandName); err == nil {
		return true
	}

	if isPlatformWindows {
		if _, _, err := findProcess(walletdCommandName + ".exe"); err == nil {
			return true
		}
		if _, _, err := findProcess(turtlecoindCommandName + ".exe"); err == nil {
			return true
		}
	}

	return false
}
