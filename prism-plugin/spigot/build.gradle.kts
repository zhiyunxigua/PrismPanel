plugins {
    id("com.gradleup.shadow")
}

dependencies {
    implementation(project(":core"))
    compileOnly("org.spigotmc:spigot-api:1.20.1-R0.1-SNAPSHOT")
}

tasks.processResources {
    filesMatching("plugin.yml") {
        expand("version" to project.version)
    }
}

tasks.named<com.github.jengelman.gradle.plugins.shadow.tasks.ShadowJar>("shadowJar") {
    archiveFileName.set("Prism-Spigot.jar")
    relocate("com.google.gson", "com.xigua.prism.libs.gson")
}

tasks.build {
    dependsOn(tasks.shadowJar)
}
