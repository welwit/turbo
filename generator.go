package turbo

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"text/template"
)

// Generator generates proto/thrift code
type Generator struct {
	RpcType        string
	PkgPath        string
	ConfigFileName string
	Options        string
	c              *Config
}

// Generate proto/thrift code
func (g *Generator) Generate() {
	if g.RpcType != "grpc" && g.RpcType != "thrift" {
		panic("Invalid server type, should be (grpc|thrift)")
	}
	g.c = NewConfig(g.RpcType, GOPATH()+"/src/"+g.PkgPath+"/"+g.ConfigFileName+".yaml")
	if g.RpcType == "grpc" {
		g.GenerateProtobufStub()
		g.c.loadFieldMapping()
		g.GenerateGrpcSwitcher()
	} else if g.RpcType == "thrift" {
		g.GenerateThriftStub()
		g.GenerateBuildThriftParameters()
		g.c.loadFieldMapping()
		g.GenerateThriftSwitcher()
	}
}

func writeFileWithTemplate(filePath string, data interface{}, text string) {
	f, err := os.Create(filePath)
	if err != nil {
		panic("fail to create file:" + filePath)
	}
	tmpl, err := template.New("").Parse(text)
	if err != nil {
		panic(err)
	}
	err = tmpl.Execute(f, data)
	if err != nil {
		panic(err)
	}
}

// GenerateGrpcSwitcher generates "grpcswither.go"
func (g *Generator) GenerateGrpcSwitcher() {
	type handlerContent struct {
		MethodNames  []string
		PkgPath      string
		ServiceName  string
		StructFields []string
	}
	if _, err := os.Stat(g.c.ServiceRootPath() + "/gen"); os.IsNotExist(err) {
		os.Mkdir(g.c.ServiceRootPath()+"/gen", 0755)
	}
	methodNames := methodNames(g.c.urlServiceMaps)
	structFields := make([]string, len(methodNames))
	for i, v := range methodNames {
		structFields[i] = g.structFields(v + "Request")
	}
	writeFileWithTemplate(
		g.c.ServiceRootPath()+"/gen/grpcswitcher.go",
		handlerContent{
			MethodNames:  methodNames,
			PkgPath:      g.PkgPath,
			ServiceName:  g.c.GrpcServiceName(),
			StructFields: structFields,
		},
		`package gen

import (
	g "{{.PkgPath}}/gen/proto"
	"github.com/vaporz/turbo"
	"reflect"
	"net/http"
	"errors"
)

/*
this is a generated file, DO NOT EDIT!
 */
// GrpcSwitcher is a runtime func with which a server starts.
var GrpcSwitcher = func(s *turbo.Server, methodName string, resp http.ResponseWriter, req *http.Request) (serviceResponse interface{}, err error) {
	switch methodName {
{{range $i, $MethodName := .MethodNames}}
	case "{{$MethodName}}":
		request := &g.{{$MethodName}}Request{ {{index $.StructFields $i}} }
		err = turbo.BuildStruct(s, reflect.TypeOf(request).Elem(), reflect.ValueOf(request).Elem(), req)
		if err != nil {
			return nil, err
		}
		return s.GrpcService().(g.{{$.ServiceName}}Client).{{$MethodName}}(req.Context(), request)
{{end}}
	default:
		return nil, errors.New("No such method[" + methodName + "]")
	}
}
`)
}

func (g *Generator) structFields(structName string) string {
	fields, ok := g.c.fieldMappings[structName]
	if !ok {
		return ""
	}
	var fieldStr string
	for _, field := range fields {
		if len(strings.TrimSpace(field)) == 0 {
			continue
		}
		pair := strings.Split(field, " ")
		nameSlice := []rune(pair[1])
		name := strings.ToUpper(string(nameSlice[0])) + string(nameSlice[1:])
		typeName := pair[0]
		fieldStr = fieldStr + name + ": &g." + typeName + "{" + g.structFields(typeName) + "},"
	}
	return fieldStr
}

// GenerateProtobufStub generates protobuf stub codes
func (g *Generator) GenerateProtobufStub() {
	if _, err := os.Stat(g.c.ServiceRootPath() + "/gen/proto"); os.IsNotExist(err) {
		os.MkdirAll(g.c.ServiceRootPath()+"/gen/proto", 0755)
	}
	cmd := "protoc " + g.Options + " --go_out=plugins=grpc:" + g.c.ServiceRootPath() + "/gen/proto" +
		" --buildfields_out=service_root_path=" + g.c.ServiceRootPath() + ":" + g.c.ServiceRootPath() + "/gen/proto"

	executeCmd("bash", "-c", cmd)
}

// GenerateBuildThriftParameters generates "build.go"
func (g *Generator) GenerateBuildThriftParameters() {
	type buildThriftParametersValues struct {
		PkgPath         string
		ServiceName     string
		ServiceRootPath string
		MethodNames     []string
	}
	writeFileWithTemplate(
		g.c.ServiceRootPath()+"/gen/thrift/build.go",
		buildThriftParametersValues{
			PkgPath:         g.PkgPath,
			ServiceName:     g.c.GrpcServiceName(),
			ServiceRootPath: g.c.ServiceRootPath(),
			MethodNames:     methodNames(g.c.urlServiceMaps)},
		buildThriftParameters,
	)
	g.runBuildThriftFields()
}

func (g *Generator) runBuildThriftFields() {
	cmd := "go run " + g.c.ServiceRootPath() + "/gen/thrift/build.go"
	c := exec.Command("bash", "-c", cmd)
	c.Stdin = os.Stdin
	c.Stderr = os.Stderr
	c.Stdout = os.Stdout
	if err := c.Run(); err != nil {
		panic(err)
	}
}

var buildThriftParameters string = `package main

import (
	"flag"
	"fmt"
	g "{{.PkgPath}}/gen/thrift/gen-go/gen"
	"io"
	"os"
	"reflect"
	"strings"
	"text/template"
)

var methodName = flag.String("n", "", "")

func main() {
	flag.Parse()
	if len(strings.TrimSpace(*methodName)) > 0 {
		str := buildParameterStr(*methodName)
		fmt.Print(str)
	} else {
		buildFields()
	}
}

func buildFields() {
	i := new(g.{{.ServiceName}})
	t := reflect.TypeOf(i).Elem()
	numMethod := t.NumMethod()
	items := make([]string, 0)
	for i := 0; i < numMethod; i++ {
		method := t.Method(i)
		numIn := method.Type.NumIn()
		for j := 0; j < numIn; j++ {
			argType := method.Type.In(j)
			argStr := argType.String()
			if argType.Kind() == reflect.Ptr && argType.Elem().Kind() == reflect.Struct {
				arr := strings.Split(argStr, ".")
				name := arr[len(arr)-1:][0]
				items = findItem(items, name, argType)
			}
		}
	}
	var list string
	for _, s := range items {
		list += s + "\n"
	}
	writeFileWithTemplate(
		"{{.ServiceRootPath}}/gen/thriftfields.yaml",
		fieldsYaml,
		fieldsYamlValues{List: list},
	)
}

func findItem(items []string, name string, structType reflect.Type) []string {
	numField := structType.Elem().NumField()
	item := "  - " + name + "["
	for i := 0; i < numField; i++ {
		fieldType := structType.Elem().Field(i)
		if fieldType.Type.Kind() == reflect.Ptr && fieldType.Type.Elem().Kind() == reflect.Struct {
			arr := strings.Split(fieldType.Type.String(), ".")
			typeName := arr[len(arr)-1:][0]
			argName := fieldType.Name
			item += fmt.Sprintf("%s %s,", typeName, argName)
			items = findItem(items, typeName, fieldType.Type)
		}
	}
	item += "]"
	return append(items, item)
}

func writeWithTemplate(wr io.Writer, text string, data interface{}) {
	tmpl, err := template.New("").Parse(text)
	if err != nil {
		panic(err)
	}
	err = tmpl.Execute(wr, data)
	if err != nil {
		panic(err)
	}
}

func writeFileWithTemplate(filePath, text string, data interface{}) {
	f, err := os.Create(filePath)
	if err != nil {
		panic("fail to create file:" + filePath)
	}
	writeWithTemplate(f, text, data)
}

type fieldsYamlValues struct {
	List string
}

var fieldsYaml string = ` + "`" + `thrift-fieldmapping:
{{printf "%s" "{{.List}}"}}
` + "`" + `

func buildParameterStr(methodName string) string {
	switch methodName {
{{range $i, $MethodName := .MethodNames}}
	case "{{$MethodName}}":
		var result string
		args := g.{{$.ServiceName}}{{$MethodName}}Args{}
		at := reflect.TypeOf(args)
		num := at.NumField()
		for i := 0; i < num; i++ {
			result += fmt.Sprintf(
				"\n\t\t\tparams[%d].Interface().(%s),",
				i, at.Field(i).Type.String())
		}
		return result
{{end}}
	default:
		return "error"
	}
}
`

// GenerateThriftSwitcher generates "thriftswitcher.go"
func (g *Generator) GenerateThriftSwitcher() {
	type thriftHandlerContent struct {
		PkgPath            string
		BuildArgsCases     string
		ServiceName        string
		MethodNames        []string
		Parameters         []string
		NotEmptyParameters []bool
		StructNames        []string
		StructFields       []string
	}
	if _, err := os.Stat(g.c.ServiceRootPath() + "/gen"); os.IsNotExist(err) {
		os.Mkdir(g.c.ServiceRootPath()+"/gen", 0755)
	}
	methodNames := methodNames(g.c.urlServiceMaps)
	parameters := make([]string, 0, len(methodNames))
	notEmptyParameters := make([]bool, 0, len(methodNames))
	for _, v := range methodNames {
		p := g.thriftParameters(v)
		parameters = append(parameters, p)
		notEmptyParameters = append(notEmptyParameters, len(strings.TrimSpace(p)) > 0)
	}

	var argCasesStr string
	fields := make([]string, 0, len(g.c.fieldMappings))
	structNames := make([]string, 0, len(g.c.fieldMappings))
	for k := range g.c.fieldMappings {
		structNames = append(structNames, k)
		fields = append(fields, g.structFields(k))
	}
	writeFileWithTemplate(
		g.c.ServiceRootPath()+"/gen/thriftswitcher.go",
		thriftHandlerContent{
			PkgPath:            g.PkgPath,
			BuildArgsCases:     argCasesStr,
			ServiceName:        g.c.ThriftServiceName(),
			MethodNames:        methodNames,
			Parameters:         parameters,
			NotEmptyParameters: notEmptyParameters,
			StructNames:        structNames,
			StructFields:       fields},
		thriftSwitcherFunc,
	)
}

func (g *Generator) thriftParameters(methodName string) string {
	cmd := "go run " + g.c.ServiceRootPath() + "/gen/thrift/build.go -n " + methodName
	buf := &bytes.Buffer{}
	c := exec.Command("bash", "-c", cmd)
	c.Stdin = os.Stdin
	c.Stderr = os.Stderr
	c.Stdout = buf
	if err := c.Run(); err != nil {
		panic(err)
	}
	return buf.String() + " "
}

func methodNames(urlServiceMaps [][3]string) []string {
	methodNamesMap := make(map[string]int)
	for _, v := range urlServiceMaps {
		methodNamesMap[v[2]] = 0
	}
	methodNames := make([]string, 0, len(methodNamesMap))
	for k := range methodNamesMap {
		methodNames = append(methodNames, k)
	}
	return methodNames
}

var thriftSwitcherFunc string = `package gen

import (
	"{{.PkgPath}}/gen/thrift/gen-go/gen"
	"github.com/vaporz/turbo"
	"reflect"
	"net/http"
	"errors"
)

/*
this is a generated file, DO NOT EDIT!
 */
// ThriftSwitcher is a runtime func with which a server starts.
var ThriftSwitcher = func(s *turbo.Server, methodName string, resp http.ResponseWriter, req *http.Request) (serviceResponse interface{}, err error) {
	switch methodName {
{{range $i, $MethodName := .MethodNames}}
	case "{{$MethodName}}":{{if index $.NotEmptyParameters $i }}
		args := gen.{{$.ServiceName}}{{$MethodName}}Args{}
		params, err := turbo.BuildArgs(s, reflect.TypeOf(args), reflect.ValueOf(args), req, buildStructArg)
		if err != nil {
			return nil, err
		}{{end}}
		return s.ThriftService().(*gen.{{$.ServiceName}}Client).{{$MethodName}}({{index $.Parameters $i}})
{{end}}
	default:
		return nil, errors.New("No such method[" + methodName + "]")
	}
}

func buildStructArg(s *turbo.Server, typeName string, req *http.Request) (v reflect.Value, err error) {
	switch typeName {
{{range $i, $StructName := .StructNames}}
	case "{{$StructName}}":
		request := &gen.{{$StructName}}{ {{index $.StructFields $i}} }
		err = turbo.BuildStruct(s, reflect.TypeOf(request).Elem(), reflect.ValueOf(request).Elem(), req)
		if err != nil {
			return v, err
		}
		return reflect.ValueOf(request), nil
{{end}}
	default:
		return v, errors.New("unknown typeName[" + typeName + "]")
	}
}
`

// GenerateThriftStub generates Thrift stub codes
func (g *Generator) GenerateThriftStub() {
	if _, err := os.Stat(g.c.ServiceRootPath() + "/gen/thrift"); os.IsNotExist(err) {
		os.MkdirAll(g.c.ServiceRootPath()+"/gen/thrift", 0755)
	}
	nameLower := strings.ToLower(g.c.ThriftServiceName())
	cmd := "thrift " + g.Options + " -r --gen go:package_prefix=" + g.PkgPath + "/gen/thrift/gen-go/ -o" +
		" " + g.c.ServiceRootPath() + "/" + "gen/thrift " + g.c.ServiceRootPath() + "/" + nameLower + ".thrift"
	executeCmd("bash", "-c", cmd)
}

func executeCmd(cmd string, args ...string) {
	// TODO learn
	c := exec.Command(cmd, args...)
	c.Stdin = os.Stdin
	c.Stderr = os.Stderr
	c.Stdout = os.Stdout
	if err := c.Run(); err != nil {
		panic(err)
	}
}
