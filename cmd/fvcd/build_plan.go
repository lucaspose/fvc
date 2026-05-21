package main

import "github.com/lucaspose/fvc/internal/buildplan"

type FvcfileConfig = buildplan.FvcfileConfig
type FvcfileImage = buildplan.FvcfileImage
type FvcfileCopy = buildplan.FvcfileCopy
type FvcfileRun = buildplan.FvcfileRun
type FvcfileRuntimeConfig = buildplan.FvcfileRuntimeConfig
type ImageRuntimeConfig = buildplan.RuntimeConfig
type BuildPlan = buildplan.Plan
type BuildCopy = buildplan.Copy
type BuildRun = buildplan.Run
type BuildIgnore = buildplan.Ignore

func LoadBuildPlan(contextPath, tagOverride string) (BuildPlan, error) {
	return buildplan.Load(contextPath, tagOverride)
}

func LoadBuildIgnore(contextPath string) (BuildIgnore, error) {
	return buildplan.LoadIgnore(contextPath)
}
