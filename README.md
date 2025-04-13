# Team Operator
A Kubernetes controller for managing namespaces.

# Overview
The Team Operator is designed to simplify the management of namespaces associated with teams in a Kubernetes cluster. Many teams often have multiple namespaces for different projects, staging, and production environments. This operator automates the process of associating namespaces with a team by creating a Team object that includes the relevant namespaces. Additionally, it adds a label snappcloud.io/team=<teamName> to the namespaces linked to the team.
