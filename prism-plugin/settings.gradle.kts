pluginManagement {
    repositories {
        gradlePluginPortal()
        mavenCentral()
    }
}

rootProject.name = "prism-plugin"

include(":core")
include(":spigot")
include(":velocity")
include(":bungee")
include(":fabric")
