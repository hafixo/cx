package main

import (
	"errors"
	"fmt"
	"io/ioutil"

	"github.com/cloud66-oss/cloud66"
	"github.com/cloud66/cli"
)

func buildStacksSSL() cli.Command {
	return cli.Command{
		Name:  "ssl",
		Usage: "commands to work with SSL certificates",
		Subcommands: []cli.Command{
			cli.Command{
				Name:   "add",
				Action: addSSLCertificate,
				Usage:  "add an SSL certificate to a stack",
				Flags: []cli.Flag{
					buildStackFlag(),
					cli.StringFlag{
						Name:  "type",
						Usage: fmt.Sprintf("type of the SSL certificate (one of '%s', or '%s')", cloud66.LetsEncryptSslCertificateType, cloud66.ManualSslCertificateType),
					},
					cli.StringFlag{
						Name:  "cert",
						Usage: fmt.Sprintf("SSL certificate file path (required for type '%s')", cloud66.ManualSslCertificateType),
					},
					cli.StringFlag{
						Name:  "key",
						Usage: fmt.Sprintf("SSL key file path (required for type '%s')", cloud66.ManualSslCertificateType),
					},
					cli.StringFlag{
						Name:  "intermediate",
						Usage: fmt.Sprintf("SSL intermediate certificate file path (optional for type '%s')", cloud66.ManualSslCertificateType),
					},
					cli.StringFlag{
						Name:  "domains",
						Usage: fmt.Sprintf("comma separated list of domain names that apply to this SSL certificate (required for type '%s', optional for type '%s')", cloud66.LetsEncryptSslCertificateType, cloud66.ManualSslCertificateType),
					},
					cli.BoolFlag{
						Name:  "overwrite",
						Usage: "update existing SSL certificate if it already exists",
					},
				},
				Description: buildStacksSSLAddDescription(),
			},
		},
	}
}

func buildStacksSSLAddDescription() string {
	return `Add an SSL certificate to a stack.

Examples:
$ cx stacks ssl add -s my-stack --type lets_encrypt --domains 'web.domain.com,api.domain.com'
$ cx stacks ssl add -s my-stack --type manual --cert certificate_file_path --key key_file_path --intermediate intermediate_file_path
`
}

func addSSLCertificate(c *cli.Context) {
	stack := mustStack(c)

	sslCertificates, err := client.ListSslCertificates(stack.Uid)
	if err != nil {
		printFatal(err.Error())
	}

	const createSSLCertificate = "create"
	const updateSSLCertificate = "update"

	sslCertificateOperation := ""
	if len(sslCertificates) != 0 {
		if !c.Bool("overwrite") {
			printFatal("SSL certificate already exists for this application. Please use the --overwrite flag if you want to overwrite the existing certificate.")
		}
		sslCertificateOperation = updateSSLCertificate
	} else {
		sslCertificateOperation = createSSLCertificate
	}

	sslCertificate, err := generateSSLCertificate(c)
	if err != nil {
		printFatal(err.Error())
	}

	var successMessage string
	switch sslCertificateOperation {
	case createSSLCertificate:
		sslCertificate, err = client.CreateSslCertificate(stack.Uid, sslCertificate)
		successMessage = "Creating SSL certificate..."
	case updateSSLCertificate:
		sslCertificateUUID := sslCertificates[0].Uuid
		sslCertificate, err = client.UpdateSslCertificate(stack.Uid, sslCertificateUUID, sslCertificate)
		successMessage = "Updating SSL certificate..."
	}
	if err != nil {
		printFatal(err.Error())
	}

	fmt.Println(successMessage)
}

func generateSSLCertificate(c *cli.Context) (*cloud66.SslCertificate, error) {
	switch c.String("type") {
	case cloud66.LetsEncryptSslCertificateType:
		return generateLetsEncryptSSLCertificate(c)
	case cloud66.ManualSslCertificateType:
		return generateManualSSLCertificate(c)
	default:
		errorMessage := fmt.Sprintf("Please ensure that you specify the SSL certificate type with the --type flag (one of '%s', or '%s').", cloud66.LetsEncryptSslCertificateType, cloud66.ManualSslCertificateType)
		return nil, errors.New(errorMessage)
	}
}

func generateLetsEncryptSSLCertificate(c *cli.Context) (*cloud66.SslCertificate, error) {
	domains := c.String("domains")
	if domains == "" {
		return nil, errors.New("No domains names specified. Please use the --domains flag to specify a list of comma separated domain names.")
	}

	return &cloud66.SslCertificate{
		Type:        cloud66.LetsEncryptSslCertificateType,
		ServerNames: domains,
	}, nil
}

func generateManualSSLCertificate(c *cli.Context) (*cloud66.SslCertificate, error) {
	certificateFile := c.String("cert")
	if certificateFile == "" {
		return nil, errors.New("No certificate file specified. Please use the --cert flag to specify it.")
	}
	certificateFileData, err := ioutil.ReadFile(certificateFile)
	if err != nil {
		return nil, err
	}
	certificate := string(certificateFileData)

	keyFile := c.String("key")
	if keyFile == "" {
		return nil, errors.New("No key file specified. Please use the --key flag to specify it.")
	}
	keyFileData, err := ioutil.ReadFile(keyFile)
	if err != nil {
		return nil, err
	}
	key := string(keyFileData)

	intermediateFile := c.String("intermediate")
	var intermediatePointer *string
	if intermediateFile != "" {
		intermediateFileData, err := ioutil.ReadFile(intermediateFile)
		if err != nil {
			return nil, err
		}
		intermediate := string(intermediateFileData)
		intermediatePointer = &intermediate
	}

	return &cloud66.SslCertificate{
		Type:        cloud66.ManualSslCertificateType,
		ServerNames: c.String("domains"),
		Certificate: &certificate,
		Key:         &key,
		IntermediateCertificate: intermediatePointer,
	}, nil
}
