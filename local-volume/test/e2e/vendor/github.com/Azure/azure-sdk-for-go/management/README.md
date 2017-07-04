# Azure Service Management packages for Go

The `github.com/Azure/azure-sdk-for-go/management` packages are used to perform operations using the Azure Service Management (ASM), aka classic deployment model. Read more about [Azure Resource Manager vs. classic deployment](https://azure.microsoft.com/documentation/articles/resource-manager-deployment-model/). Packages for Azure Resource Manager are in the [arm](../arm) folder.

## First a Sidenote: Authentication and the Azure Service Manager

The client currently supports authentication to the Service Management
API with certificates or Azure `.publishSettings` file. You can 
download the `.publishSettings` file for your subscriptions
[here](https://manage.windowsazure.com/publishsettings).

### Example: Creating a Linux Virtual Machine

Complete source code for this example can be found in [example.go](/management/examples/example.go).To try this example, [download your .publishSettings](https://manage.windowsazure.com/publishsettings) and add its path in the first line of the main function. Run the example with commands:

```
$ cd management/examples
$ go run example.go
```