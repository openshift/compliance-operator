---
Title: Operator Introduction
PrevPage: ../index
NextPage: 02-installation
---

Compliance Operator
===================

The Compliance Operator lets administrators describe the desired compliance
state of a cluster, and works natively with OCP to try to bring the cluster
into compliance automatically.  Along the way, the operator provides feedback
to the administrator to help resolve any problems or gaps in the policy,
including manual remediations.

Under the hood, the operator assesses compliance of both the Kubernetes part of
OpenShift as well as the nodes running the cluster with the NIST-certified
OpenSCAP tool.  OpenSCAP makes a compliance assessment based on the policy
rules defined by the content.

The operator also provides the means to continuously evaluate, making sure that
the cluster will stay in compliance; You'll always have up-to-date scan results
to show to your auditor.

***

[Let's install the operator!](02-installation.md)
