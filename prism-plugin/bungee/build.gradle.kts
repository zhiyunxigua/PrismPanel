plugins {
    id("com.gradleup.shadow")
}

dependencies {
    implementation(project(":core"))
    // Waterfall 是 BungeeCord 兼容分支，提供同名 net.md_5.bungee API（papermc 仓库可达）。
    compileOnly("io.github.waterfallmc:waterfall-api:1.21-R0.1-SNAPSHOT")
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
