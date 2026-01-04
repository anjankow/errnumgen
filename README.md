# errnumgen

## Parsing and rewriting all errors in your application

Errnumgen provides a way to parse your application, finding all returned errors and modifying them.
`errparser` allows you to specify your own generator implementation to handle found errors.

## Why?

To add a custom wrapper to each error or modify it according to your needs.

Thanks to AST parsing, you're assured that all errors will be found,
no matter how they are represented in the code (e.g. returning a struct or a function call).

## Usage

```
go run errnumgen.go -out-pkg=my-errnums ./pkg/errparser/testdata
```
This will create an output file with the default file name and belonging to the package `my-errnums`.
The output file will contain the templated content (`pkg/generator/errnums.tmpl`) with the enumeration consts.

`./pkg/errparser/testdata` is the directory that will be recursively parsed searching for the errors and updated.

## Parser

The `errparser` goes through each file in a directory and finds all returned errors.
It calls the provided error node handler on each one, letting the caller, for example, enumerate the errors.

## Generator

The already provided generator will enumerate all errors within the application and
add a custom error wrapper that assigns a unique number to each one.
The error wrapper also adds the error frame to be used when debugging.

### What's the purpose of enumeration?

Oftentimes you wouldn't care about adding meaningful error messages, especially when errors
travel through many layers and at each one we simply write:

```
if err := operation(); err != nil {
    return err   // we lose this trace
}
```

When returning the error to the user, the error path is completely lost in this case.

How can we make it more visible?
We can add a wrapper that will capture the stack frame at each layer.
But we wouldn't like to expose the stack trace to the user — this is where the idea of enumeration comes from.

At the top level, when returning the error to the user, the method `Code` can be called.
This function will find all the hardcoded error codes from each layer and return them as a single error code.
Then, they can be easily found within the code if an error gets reported.

```
if err := operation(); err != nil {
    return errnums.New(errnums.N_34, err)
}
```
