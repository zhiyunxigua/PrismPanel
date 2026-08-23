plugins {
    id("com.gradleup.shadow")
}

dependencies {
    implementation(project(":core"))
    compileOnly("com.velocitypowered:velocity-api:3.4.0-SNAPSHOT")
    annotationProcessor("com.velocitypowered:velocity-api:3.4.0-SNAPSHOT")
}

tasks.processResources {
    filesMatching("velocity-plugin.json") {
        expand("version" to project.version)
    }
}

tasks.named<com.github.jengelman.gradle.plugins.shadow.tasks.ShadowJar>("shadowJar") {
    archiveFileName.set("Prism-Velocity.jar")
    relocate("com.google.gson", "com.xigua.prism.libs.gson")
}

tasks.build {
    dependsOn(tasks.shadowJar)
}
