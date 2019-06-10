package corev1alpha1

import "github.com/awesome-flow/flow/pkg/types"

type Params map[string]types.Value

type Constructor func(name string, ctx *Context, params Params) (Actor, error)
