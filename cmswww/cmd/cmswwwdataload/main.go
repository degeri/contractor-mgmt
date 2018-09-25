package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	cliconfig "github.com/decred/contractor-mgmt/cmswww/cmd/cmswwwcli/config"
	wwwconfig "github.com/decred/contractor-mgmt/cmswww/sharedconfig"
	"github.com/decred/dcrd/chaincfg"
	"github.com/decred/dcrd/dcrutil"

	"github.com/decred/contractor-mgmt/cmswww/cmd/cmswwwdataload/client"
	"github.com/decred/contractor-mgmt/cmswww/cmd/cmswwwdataload/config"
)

type csvWorkRecord struct {
	typeOfWork    string
	subtypeOfWork string
	description   string
	hours         uint
	totalCost     float64
}

var (
	cfg          *config.Config
	c            *client.Client
	politeiadCmd *exec.Cmd
	cmswwwCmd    *exec.Cmd
)

func createLogFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0644)
}

func createInvoiceFile(month, year uint16, numRecords int) (string, error) {
	date := time.Date(int(year), time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	filepath := path.Join(cfg.DataDir, date.Format("2006-01.csv"))
	file, err := os.OpenFile(filepath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return "", err
	}

	file.WriteString(fmt.Sprintf("# %v\n", date.Format("2006-01")))
	file.WriteString("# This file was generated by the dataload utility.\n")
	for i := 1; i <= numRecords; i++ {
		file.WriteString(fmt.Sprintf("Development,Task %v,,%v,%v", i, 20, 20*i))
	}
	return filepath, file.Sync()
}

func waitForStartOfDay(out io.Reader) {
	buf := bufio.NewScanner(out)
	for buf.Scan() {
		text := buf.Text()
		if strings.Contains(text, "Start of day") {
			return
		}
	}
}

func startCmswww() error {
	fmt.Printf("Starting cmswww\n")
	cmswwwCmd = c.CreateCmswwwCmd()

	stdout, _ := cmswwwCmd.StdoutPipe()
	if err := cmswwwCmd.Start(); err != nil {
		cmswwwCmd = nil
		return err
	}

	logFile, err := createLogFile(cfg.CmswwwLogFile)
	if err != nil {
		return err
	}

	reader := io.TeeReader(stdout, logFile)
	waitForStartOfDay(reader)
	go io.Copy(logFile, stdout)

	// Get the version for the csrf
	return c.Version()
}

func startPoliteiad() error {
	fmt.Printf("Starting politeiad\n")
	politeiadCmd = c.ExecuteCommand("politeiad", "--testnet")
	out, _ := politeiadCmd.StdoutPipe()
	if err := politeiadCmd.Start(); err != nil {
		politeiadCmd = nil
		return err
	}

	logFile, err := createLogFile(cfg.PoliteiadLogFile)
	if err != nil {
		return err
	}

	reader := io.TeeReader(out, logFile)
	waitForStartOfDay(reader)
	return nil
}

func setNewIdentity(email, password string) error {
	if _, err := c.Login(email, password); err != nil {
		return err
	}

	token, err := c.NewIdentity()
	if err != nil {
		return err
	}

	if err = c.VerifyIdentity(token); err != nil {
		return err
	}

	return c.Logout()
}

func submitInvoice(email, password, filepath string) (string, error) {
	if _, err := c.Login(email, password); err != nil {
		return "", err
	}

	token, err := c.SubmitInvoice(filepath)
	if err != nil {
		return "", err
	}

	return token, c.Logout()
}

func approveInvoice(email, password, token string) error {
	if _, err := c.Login(email, password); err != nil {
		return err
	}

	err := c.ApproveInvoice(token)
	if err != nil {
		return err
	}

	return c.Logout()
}

func rejectInvoice(email, password, token string) error {
	if _, err := c.Login(email, password); err != nil {
		return err
	}

	err := c.RejectInvoice(token)
	if err != nil {
		return err
	}

	return c.Logout()
}

func testPasswordResetAndChange(email, password string) error {
	newPassword := generateRandomString(16)
	if err := c.ResetPassword(email, newPassword); err != nil {
		return err
	}

	if _, err := c.Login(email, newPassword); err != nil {
		return err
	}

	if err := c.ChangePassword(newPassword, password); err != nil {
		return err
	}

	return c.Logout()
}

func testEditUser(email, password string) error {
	lr, err := c.Login(email, password)
	if err != nil {
		return err
	}

	udr, err := c.UserDetails(lr.UserID)
	if err != nil {
		return err
	}

	newName := generateRandomString(16)
	newLocation := generateRandomString(16)
	newExtendedPublicKey := generateRandomString(16)

	if err := c.EditUser(newName, newLocation, newExtendedPublicKey); err != nil {
		return err
	}

	udr2, err := c.UserDetails(lr.UserID)
	if err != nil {
		return err
	}

	if newName != udr2.User.Name {
		return fmt.Errorf("User's name was not modified")
	}
	if newLocation != udr2.User.Location {
		return fmt.Errorf("User's location was not modified")
	}
	if newExtendedPublicKey != udr2.User.ExtendedPublicKey {
		return fmt.Errorf("User's extended public key was not modified")
	}

	if err := c.EditUser(udr.User.Name, udr.User.Location, udr.User.ExtendedPublicKey); err != nil {
		return err
	}

	return c.Logout()
}

func createContractorUser(
	adminEmail,
	adminPass,
	contractorEmail,
	contractorUser,
	contractorPass,
	contractorName,
	contractorLocation,
	contractorExtendedPublicKey string,
) error {
	if _, err := c.Login(adminEmail, adminPass); err != nil {
		return err
	}

	_, err := c.InviteUser(contractorEmail)
	if err != nil {
		return err
	}

	token, err := c.ResendInvite(contractorEmail)
	if err != nil {
		return err
	}

	if err = c.Logout(); err != nil {
		return err
	}

	return c.RegisterUser(
		contractorEmail,
		contractorUser,
		contractorPass,
		contractorName,
		contractorLocation,
		contractorExtendedPublicKey,
		token)
}

func deleteExistingData() error {
	fmt.Printf("Deleting existing data\n")

	// politeiad data dir
	politeiadDataDir := filepath.Join(dcrutil.AppDataDir("politeiad", false), "data")
	if err := os.RemoveAll(politeiadDataDir); err != nil {
		return err
	}

	// cmswww data dir
	testnetDataDir := filepath.Join(wwwconfig.DefaultDataDir,
		chaincfg.TestNet3Params.Name)
	os.RemoveAll(filepath.Join(testnetDataDir, "sessions"))
	os.Remove(filepath.Join(testnetDataDir, "csrf.key"))

	// cmswww db
	if err := c.DeleteAllData(); err != nil {
		return err
	}

	// cmswww cli dir
	os.RemoveAll(cliconfig.HomeDir)
	return nil
}

func stopPoliteiad() {
	if politeiadCmd != nil {
		fmt.Printf("Stopping politeiad\n")
		politeiadCmd.Process.Kill()
		politeiadCmd = nil
	}
}

func stopCmswww() {
	if cmswwwCmd != nil {
		fmt.Printf("Stopping cmswww\n")
		cmswwwCmd.Process.Kill()
		cmswwwCmd = nil
	}
}

func stopServers() {
	stopPoliteiad()
	stopCmswww()
}

func _main() error {
	// Load configuration and parse command line.  This function also
	// initializes logging and configures it accordingly.
	var err error
	cfg, err = config.Load()
	if err != nil {
		return fmt.Errorf("Could not load configuration file: %v", err)
	}

	c = client.NewClient(cfg)

	if cfg.DeleteData {
		if err = deleteExistingData(); err != nil {
			return err
		}
	}

	err = c.CreateAdminUser(cfg.AdminEmail, cfg.AdminUser, cfg.AdminPass)
	if err != nil {
		return err
	}

	if err = startPoliteiad(); err != nil {
		return err
	}

	if err = startCmswww(); err != nil {
		return err
	}

	if err = setNewIdentity(cfg.AdminEmail, cfg.AdminPass); err != nil {
		return err
	}

	err = createContractorUser(
		cfg.AdminEmail,
		cfg.AdminPass,
		cfg.ContractorEmail,
		cfg.ContractorUser,
		cfg.ContractorPass,
		cfg.ContractorName,
		cfg.ContractorLocation,
		cfg.ContractorExtendedPublicKey,
	)
	if err != nil {
		return err
	}

	if err = setNewIdentity(cfg.ContractorEmail, cfg.ContractorPass); err != nil {
		return err
	}

	invoiceToApproveFilepath, err := createInvoiceFile(9, 2018, 5)
	if err != nil {
		return err
	}
	invoiceToApproveToken, err := submitInvoice(cfg.ContractorEmail, cfg.ContractorPass,
		invoiceToApproveFilepath)
	if err != nil {
		return err
	}

	err = approveInvoice(cfg.AdminEmail, cfg.AdminPass, invoiceToApproveToken)
	if err != nil {
		return err
	}

	invoiceToRejectFilepath, err := createInvoiceFile(10, 2018, 5)
	if err != nil {
		return err
	}
	invoiceToRejectToken, err := submitInvoice(cfg.ContractorEmail, cfg.ContractorPass,
		invoiceToRejectFilepath)
	if err != nil {
		return err
	}

	err = rejectInvoice(cfg.AdminEmail, cfg.AdminPass, invoiceToRejectToken)
	if err != nil {
		return err
	}

	if cfg.IncludeTests {
		err = testPasswordResetAndChange(cfg.AdminEmail, cfg.AdminPass)
		if err != nil {
			return err
		}

		err = testEditUser(cfg.ContractorEmail, cfg.ContractorPass)
		if err != nil {
			return err
		}
	}

	fmt.Printf("Load data complete\n")
	return nil
}

func main() {
	err := _main()
	stopServers()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
