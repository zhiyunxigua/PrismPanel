plugins {
    id("com.gradleup.shadow")
}

dependencies {
    implementation(project(":core"))
    compileOnly("net.md-5:bungeecord-proxy:1.21-R0.1-SNAPSHOT")
}

tasks.processResources {
    filesMatching("bungee.yml") {
        expand("version" to project.version)
    }
}

tasks.named<com.github.jengelman.gradle.plugins.shadow.tasks.ShadowJar>("shadowJar") {
    archiveFileName.set("Prism-Bungee.jar")
    relocate("com.google.gson", "com.xigua.prism.libs.gson")
}

tasks.build {
    dependsOn(tasks.shadowJar)
}
